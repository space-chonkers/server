package marsgeo

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// HiRISE DTM dataset identifier (label DATA_SET_ID).
const HiRISEDTMDatasetID = "MRO-M-HIRISE-5-DTM-V1.0"

// pdsValue is one keyword value: the raw text (quotes removed for strings)
// and the optional <UNIT> suffix.
type pdsValue struct {
	raw    string
	unit   string
	quoted bool
}

// pdsLabel is a parsed PDS3 label: keywords keyed by "OBJECT.KEY" (top level
// keys have no prefix), plus the byte length of the label text up to and
// including the END statement.
type pdsLabel struct {
	values map[string]pdsValue
	endAt  int
}

// parsePDSLabel parses PDS3 ODL from the start of data up to the END
// statement: KEY = value statements, OBJECT/END_OBJECT nesting, /* */
// comments, multi-line quoted strings, (a, b) lists, <UNIT> suffixes and
// radix values such as 16#FF7FFFFB#. Duplicate keys within one object, a
// missing END and malformed statements are errors.
func parsePDSLabel(data []byte) (*pdsLabel, error) {
	l := &pdsLabel{values: map[string]pdsValue{}}
	var stack []string
	i, n := 0, len(data)
	skipSpace := func(stopAtNewline bool) {
		for i < n {
			c := data[i]
			switch {
			case c == '\n' && stopAtNewline:
				return
			case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == 0:
				i++
			case c == '/' && i+1 < n && data[i+1] == '*':
				end := bytes.Index(data[i+2:], []byte("*/"))
				if end < 0 {
					i = n
					return
				}
				i += end + 4
			default:
				return
			}
		}
	}
	readWord := func() string {
		start := i
		for i < n {
			c := data[i]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '=' || c == 0 || (c == '/' && i+1 < n && data[i+1] == '*') {
				break
			}
			i++
		}
		return string(data[start:i])
	}
	for {
		skipSpace(false)
		if i >= n {
			return nil, fmt.Errorf("%w: label has no END statement", ErrInvalidProduct)
		}
		key := readWord()
		if key == "" {
			return nil, fmt.Errorf("%w: malformed label near byte %d", ErrInvalidProduct, i)
		}
		if key == "END" {
			l.endAt = i
			if len(stack) != 0 {
				return nil, fmt.Errorf("%w: END inside OBJECT %s", ErrInvalidProduct, stack[len(stack)-1])
			}
			return l, nil
		}
		skipSpace(false)
		if i >= n || data[i] != '=' {
			return nil, fmt.Errorf("%w: keyword %s has no '='", ErrInvalidProduct, key)
		}
		i++
		skipSpace(false)
		if i >= n {
			return nil, fmt.Errorf("%w: keyword %s has no value", ErrInvalidProduct, key)
		}
		var v pdsValue
		switch data[i] {
		case '"':
			end := bytes.IndexByte(data[i+1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated string for %s", ErrInvalidProduct, key)
			}
			v = pdsValue{raw: string(data[i+1 : i+1+end]), quoted: true}
			i += end + 2
		case '(', '{':
			closer := byte(')')
			if data[i] == '{' {
				closer = '}'
			}
			end := bytes.IndexByte(data[i:], closer)
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated list for %s", ErrInvalidProduct, key)
			}
			v = pdsValue{raw: string(data[i : i+end+1])}
			i += end + 1
		default:
			v = pdsValue{raw: readWord()}
		}
		skipSpace(true)
		if i < n && data[i] == '<' {
			end := bytes.IndexByte(data[i:], '>')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated unit for %s", ErrInvalidProduct, key)
			}
			v.unit = string(data[i+1 : i+end])
			i += end + 1
		}
		switch key {
		case "OBJECT":
			stack = append(stack, v.raw)
			continue
		case "END_OBJECT":
			if len(stack) == 0 || stack[len(stack)-1] != v.raw {
				return nil, fmt.Errorf("%w: END_OBJECT = %s does not close the open object", ErrInvalidProduct, v.raw)
			}
			stack = stack[:len(stack)-1]
			continue
		}
		full := key
		if len(stack) > 0 {
			full = strings.Join(stack, ".") + "." + key
		}
		if _, dup := l.values[full]; dup {
			return nil, fmt.Errorf("%w: duplicate keyword %s", ErrInvalidProduct, full)
		}
		l.values[full] = v
	}
}

func (l *pdsLabel) get(key string) (pdsValue, error) {
	v, ok := l.values[key]
	if !ok {
		return pdsValue{}, fmt.Errorf("%w: missing keyword %s", ErrInvalidProduct, key)
	}
	return v, nil
}

func (l *pdsLabel) has(key string) bool { _, ok := l.values[key]; return ok }

func (l *pdsLabel) str(key string) (string, error) {
	v, err := l.get(key)
	return v.raw, err
}

func (l *pdsLabel) integer(key string) (int64, error) {
	v, err := l.get(key)
	if err != nil {
		return 0, err
	}
	x, perr := strconv.ParseInt(v.raw, 10, 64)
	if perr != nil {
		return 0, fmt.Errorf("%w: %s = %q is not an integer", ErrInvalidProduct, key, v.raw)
	}
	return x, nil
}

func (l *pdsLabel) positiveInt(key string) (int, error) {
	x, err := l.integer(key)
	if err != nil {
		return 0, err
	}
	if x <= 0 || x > math.MaxInt32 {
		return 0, fmt.Errorf("%w: %s = %d must be a positive integer", ErrInvalidProduct, key, x)
	}
	return int(x), nil
}

// real parses a finite number, requiring the given unit ("" = no unit allowed).
func (l *pdsLabel) real(key string, units ...string) (float64, error) {
	v, err := l.get(key)
	if err != nil {
		return 0, err
	}
	x, perr := strconv.ParseFloat(v.raw, 64)
	if perr != nil || math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, fmt.Errorf("%w: %s = %q is not a finite number", ErrInvalidProduct, key, v.raw)
	}
	if len(units) > 0 {
		ok := false
		for _, u := range units {
			if strings.EqualFold(v.unit, u) {
				ok = true
			}
		}
		if !ok {
			return 0, fmt.Errorf("%w: %s has unit <%s>, want one of %v", ErrInvalidProduct, key, v.unit, units)
		}
	}
	return x, nil
}

// Label is the subset of a HiRISE DTM PDS3 label this package uses, after
// verification. All values are read from the product's own label.
type Label struct {
	ProductID        string
	ProductVersionID string
	DatasetID        string
	SoftwareName     string
	Note             string

	RecordBytes int
	LabelBytes  int   // bytes of label text up to and including END
	ImageOffset int64 // byte offset of the first image sample
	Lines       int
	Samples     int // LINE_SAMPLES
	SampleType  string
	SampleBits  int

	MissingConstantBits uint32
	ValidMinimum        float64
	ValidMaximum        float64

	ProjectionType  string
	RadiusMeters    float64 // A_AXIS_RADIUS, the product's projection sphere
	CenterLatitude  float64 // standard parallel
	CenterLongitude float64
	MapScale        float64 // metres per pixel
	LineOffset      float64 // LINE_PROJECTION_OFFSET
	SampleOffset    float64 // SAMPLE_PROJECTION_OFFSET

	MinimumLatitude, MaximumLatitude           float64
	WesternmostLongitude, EasternmostLongitude float64
}

// ParseLabel parses and verifies a HiRISE DTEE* DTM label from the start of
// a product file. Every binary-layout and projection rule of docs/MARSGEO.md
// is checked; anything unsupported is rejected with ErrInvalidProduct.
func ParseLabel(data []byte) (*Label, error) {
	pl, err := parsePDSLabel(data)
	if err != nil {
		return nil, err
	}
	return verifyLabel(pl)
}

func verifyLabel(pl *pdsLabel) (*Label, error) {
	var lab Label
	var errs []error
	check := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	fail := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("%w: "+format, append([]any{ErrInvalidProduct}, a...)...))
	}
	want := func(key, value string) {
		v, err := pl.str(key)
		if err != nil {
			check(err)
			return
		}
		if v != value {
			fail("%s = %q, want %q", key, v, value)
		}
	}

	want("PDS_VERSION_ID", "PDS3")
	want("RECORD_TYPE", "FIXED_LENGTH")
	var err error
	lab.RecordBytes, err = pl.positiveInt("RECORD_BYTES")
	check(err)
	lab.LabelBytes = pl.endAt

	// ^IMAGE: record pointer n, or "n <BYTES>". Detached files are rejected.
	if v, err := pl.get("^IMAGE"); err != nil {
		check(err)
	} else {
		n, perr := strconv.ParseInt(v.raw, 10, 64)
		switch {
		case v.quoted || strings.HasPrefix(v.raw, "("):
			fail("detached ^IMAGE pointer %s is not supported", v.raw)
		case perr != nil || n < 1:
			fail("^IMAGE = %q is not a positive integer", v.raw)
		case v.unit == "":
			lab.ImageOffset = (n - 1) * int64(lab.RecordBytes)
		case strings.EqualFold(v.unit, "BYTES"):
			lab.ImageOffset = n - 1
		default:
			fail("^IMAGE unit <%s> not supported", v.unit)
		}
	}

	lab.DatasetID, err = pl.str("DATA_SET_ID")
	check(err)
	if err == nil && lab.DatasetID != HiRISEDTMDatasetID {
		fail("DATA_SET_ID = %q, want %q", lab.DatasetID, HiRISEDTMDatasetID)
	}
	lab.ProductID, err = pl.str("PRODUCT_ID")
	check(err)
	if err == nil && !strings.HasPrefix(lab.ProductID, "DTEE") {
		fail("PRODUCT_ID %q is not a DTEE* (equirectangular, areoid) product", lab.ProductID)
	}
	if err == nil && !isPrintableASCII(lab.ProductID) {
		fail("PRODUCT_ID %q is not printable ASCII", lab.ProductID)
	}
	lab.ProductVersionID, _ = pl.str("PRODUCT_VERSION_ID")
	lab.SoftwareName, _ = pl.str("SOFTWARE_NAME")

	// IMAGE object: binary layout.
	lab.Lines, err = pl.positiveInt("IMAGE.LINES")
	check(err)
	lab.Samples, err = pl.positiveInt("IMAGE.LINE_SAMPLES")
	check(err)
	lab.SampleType, err = pl.str("IMAGE.SAMPLE_TYPE")
	check(err)
	bits, err := pl.positiveInt("IMAGE.SAMPLE_BITS")
	check(err)
	lab.SampleBits = bits
	if lab.SampleType != "" && (lab.SampleType != "PC_REAL" || bits != 32) {
		fail("SAMPLE_TYPE %s with SAMPLE_BITS %d is not supported (only PC_REAL, 32)", lab.SampleType, bits)
	}
	if pl.has("IMAGE.BANDS") {
		if b, err := pl.integer("IMAGE.BANDS"); err != nil || b != 1 {
			fail("BANDS must be 1")
		}
	}
	for _, k := range []string{"IMAGE.LINE_PREFIX_BYTES", "IMAGE.LINE_SUFFIX_BYTES"} {
		if pl.has(k) {
			if b, err := pl.integer(k); err != nil || b != 0 {
				fail("%s must be 0", k)
			}
		}
	}
	if sf, err := pl.real("IMAGE.SCALING_FACTOR"); err != nil {
		check(err)
	} else if sf != 1 {
		fail("SCALING_FACTOR = %v, only 1.0 is supported", sf)
	}
	if off, err := pl.real("IMAGE.OFFSET"); err != nil {
		check(err)
	} else if off != 0 {
		fail("OFFSET = %v, only 0.0 is supported", off)
	}
	if mc, err := pl.str("IMAGE.MISSING_CONSTANT"); err != nil {
		check(err)
	} else if mc != "16#FF7FFFFB#" {
		fail("MISSING_CONSTANT = %s, only 16#FF7FFFFB# (ISIS NULL4) is supported", mc)
	} else {
		lab.MissingConstantBits = isisNull4Bits
	}
	lab.ValidMinimum, err = pl.real("IMAGE.VALID_MINIMUM")
	check(err)
	lab.ValidMaximum, err = pl.real("IMAGE.VALID_MAXIMUM")
	check(err)
	if lab.ValidMinimum > lab.ValidMaximum {
		fail("VALID_MINIMUM %v > VALID_MAXIMUM %v", lab.ValidMinimum, lab.ValidMaximum)
	}
	lab.Note, err = pl.str("IMAGE.NOTE")
	check(err)
	if err == nil && !strings.Contains(strings.Join(strings.Fields(lab.Note), " "), "Mars 2000") {
		fail("IMAGE.NOTE does not name the Mars 2000 datum")
	}
	if lab.RecordBytes > 0 && lab.Samples > 0 && lab.RecordBytes != lab.Samples*4 {
		fail("RECORD_BYTES %d != LINE_SAMPLES %d x 4", lab.RecordBytes, lab.Samples)
	}
	if lab.ImageOffset > 0 && lab.ImageOffset < int64(lab.LabelBytes) {
		fail("image offset %d overlaps the label (%d bytes)", lab.ImageOffset, lab.LabelBytes)
	}

	// IMAGE_MAP_PROJECTION.
	const p = "IMAGE_MAP_PROJECTION."
	want(p+"MAP_PROJECTION_TYPE", "EQUIRECTANGULAR")
	lab.ProjectionType = "EQUIRECTANGULAR"
	want(p+"COORDINATE_SYSTEM_NAME", "PLANETOCENTRIC")
	want(p+"POSITIVE_LONGITUDE_DIRECTION", "EAST")
	if rot, err := pl.real(p+"MAP_PROJECTION_ROTATION", "DEG", ""); err != nil {
		check(err)
	} else if rot != 0 {
		fail("MAP_PROJECTION_ROTATION = %v, only 0 is supported", rot)
	}
	a, aErr := pl.real(p+"A_AXIS_RADIUS", "KM")
	check(aErr)
	lab.RadiusMeters = a * 1000
	for _, k := range []string{"B_AXIS_RADIUS", "C_AXIS_RADIUS"} {
		if r, err := pl.real(p+k, "KM"); aErr == nil && err == nil && r != a {
			fail("%s %v != A_AXIS_RADIUS %v (equirectangular needs a sphere)", k, r, a)
		}
	}
	if aErr == nil && lab.RadiusMeters <= 0 {
		fail("A_AXIS_RADIUS must be > 0")
	}
	lab.CenterLatitude, err = pl.real(p+"CENTER_LATITUDE", "DEG")
	check(err)
	if math.Abs(lab.CenterLatitude) >= 90 {
		fail("CENTER_LATITUDE %v out of range", lab.CenterLatitude)
	}
	lab.CenterLongitude, err = pl.real(p+"CENTER_LONGITUDE", "DEG")
	check(err)
	lab.MapScale, err = pl.real(p+"MAP_SCALE", "METERS/PIXEL", "METERS/PIX")
	check(err)
	if err == nil && lab.MapScale <= 0 {
		fail("MAP_SCALE must be > 0")
	}
	lab.LineOffset, err = pl.real(p+"LINE_PROJECTION_OFFSET", "PIXEL", "PIX")
	check(err)
	lab.SampleOffset, err = pl.real(p+"SAMPLE_PROJECTION_OFFSET", "PIXEL", "PIX")
	check(err)
	lab.MinimumLatitude, err = pl.real(p+"MINIMUM_LATITUDE", "DEG")
	check(err)
	lab.MaximumLatitude, err = pl.real(p+"MAXIMUM_LATITUDE", "DEG")
	check(err)
	lab.WesternmostLongitude, err = pl.real(p+"WESTERNMOST_LONGITUDE", "DEG")
	check(err)
	lab.EasternmostLongitude, err = pl.real(p+"EASTERNMOST_LONGITUDE", "DEG")
	check(err)

	if len(errs) > 0 {
		return nil, joinErrors(errs)
	}
	return &lab, nil
}

// joinErrors keeps ErrInvalidProduct matchable while listing every problem.
func joinErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = strings.TrimPrefix(e.Error(), ErrInvalidProduct.Error()+": ")
	}
	return fmt.Errorf("%w: %s", ErrInvalidProduct, strings.Join(msgs, "; "))
}

func isPrintableASCII(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// ImageBytes is the byte length of the image the label declares.
func (l *Label) ImageBytes() int64 { return int64(l.Lines) * int64(l.Samples) * 4 }
