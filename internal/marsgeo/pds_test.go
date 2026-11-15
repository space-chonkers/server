package marsgeo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

const a02LabelFile = "DTEED_029716_1980_029426_1980_A02.LBL"

func readA02Label(t testing.TB) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hirise", a02LabelFile))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRealA02LabelParses(t *testing.T) {
	lab, err := ParseLabel(readA02Label(t))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"ProductID", lab.ProductID, "DTEED_029716_1980_029426_1980_A02"},
		{"ProductVersionID", lab.ProductVersionID, "2"},
		{"DatasetID", lab.DatasetID, HiRISEDTMDatasetID},
		{"SampleType", lab.SampleType, "PC_REAL"},
		{"SampleBits", lab.SampleBits, 32},
		{"RecordBytes", lab.RecordBytes, 14016},
		{"ImageOffset", lab.ImageOffset, int64(14016)},
		{"Lines", lab.Lines, 8252},
		{"Samples", lab.Samples, 3504},
		{"MissingConstantBits", lab.MissingConstantBits, uint32(0xFF7FFFFB)},
		{"ValidMinimum", lab.ValidMinimum, 916.06},
		{"ValidMaximum", lab.ValidMaximum, 1041.58},
		{"RadiusMeters", lab.RadiusMeters, 3394839.8133163},
		{"CenterLatitude", lab.CenterLatitude, 15.0},
		{"CenterLongitude", lab.CenterLongitude, 232.49},
		{"MapScale", lab.MapScale, 2.0195488956581},
		{"LineOffset", lab.LineOffset, 527163.49999999},
		{"SampleOffset", lab.SampleOffset, 1864.5},
		{"SoftwareName", lab.SoftwareName, "SOCET Set 5.6.0"},
		{"ImageBytes", lab.ImageBytes(), int64(115674048 - 14016)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if lab.LabelBytes > int(lab.ImageOffset) {
		t.Errorf("label text (%d bytes) overlaps image offset", lab.LabelBytes)
	}
}

func TestImagePointerForms(t *testing.T) {
	for _, tc := range []struct {
		name string
		ptr  func(rb, records int) (string, int)
	}{
		{"record pointer 3", func(rb, records int) (string, int) { return "3", 2 * rb }},
		{"byte pointer", func(rb, records int) (string, int) { return "28033 <BYTES>", 28032 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newSynthProduct(4000, 3) // RECORD_BYTES 16000, so the label fits in one record
			p.imagePointer = tc.ptr
			p.set(0, 0, math.Float32bits(123.25)) // marker
			prod := p.open(t)
			_, want := tc.ptr(16000, 1)
			if prod.Label.ImageOffset != int64(want) {
				t.Fatalf("offset = %d want %d", prod.Label.ImageOffset, want)
			}
			w, err := prod.ReadWindow(0, 0, 2, 2)
			if err != nil {
				t.Fatal(err)
			}
			if _, v := w.At(0, 0); v != 123.25 {
				t.Fatalf("marker = %v", v)
			}
			if _, v := w.At(1, 1); v != 1001 {
				t.Fatalf("pixel (1,1) = %v want 1001", v)
			}
		})
	}
}

func expectInvalidProduct(t *testing.T, p *synthProduct) {
	t.Helper()
	b := p.bytes()
	_, err := OpenProduct(bytes.NewReader(b), int64(len(b)))
	if !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("err = %v, want ErrInvalidProduct", err)
	}
	t.Log(err)
}

func TestLabelStructureErrors(t *testing.T) {
	cases := map[string]func(p *synthProduct){
		"missing RECORD_BYTES":     func(p *synthProduct) { p.edit("RECORD_BYTES", "") },
		"missing LINES":            func(p *synthProduct) { p.edit("LINES", "") },
		"missing SAMPLE_TYPE":      func(p *synthProduct) { p.edit("SAMPLE_TYPE", "") },
		"missing SAMPLE_BITS":      func(p *synthProduct) { p.edit("SAMPLE_BITS", "") },
		"missing ^IMAGE":           func(p *synthProduct) { p.edit("^IMAGE", "") },
		"missing MISSING_CONSTANT": func(p *synthProduct) { p.edit("MISSING_CONSTANT", "") },
		"missing VALID_MINIMUM":    func(p *synthProduct) { p.edit("VALID_MINIMUM", "") },
		"missing MAP_SCALE":        func(p *synthProduct) { p.edit("MAP_SCALE", "") },
		"duplicate LINES":          func(p *synthProduct) { p.appendLine("LINES", "  LINES = 8") },
		"non-integer RECORD_BYTES": func(p *synthProduct) { p.edit("RECORD_BYTES", "RECORD_BYTES = 40.5") },
		"negative LINES":           func(p *synthProduct) { p.edit("LINES", "  LINES = -8") },
		"VALID_MIN > VALID_MAX":    func(p *synthProduct) { p.ValidMin, p.ValidMax = 10, 5 },
		"no PDS3":                  func(p *synthProduct) { p.edit("PDS_VERSION_ID", "PDS_VERSION_ID = PDS4") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := newSynthProduct(4, 4)
			mutate(p)
			expectInvalidProduct(t, p)
		})
	}
	if _, err := ParseLabel([]byte("PDS_VERSION_ID = PDS3\r\nRECORD_BYTES = 4\r\n")); !errors.Is(err, ErrInvalidProduct) {
		t.Errorf("label without END accepted: %v", err)
	}
}

func TestUnsupportedFormatsRejected(t *testing.T) {
	cases := map[string]func(p *synthProduct){
		"IEEE_REAL/32": func(p *synthProduct) { p.edit("SAMPLE_TYPE", "  SAMPLE_TYPE = IEEE_REAL") },
		"MSB_INTEGER/16": func(p *synthProduct) {
			p.edit("SAMPLE_TYPE", "  SAMPLE_TYPE = MSB_INTEGER").edit("SAMPLE_BITS", "  SAMPLE_BITS = 16")
		},
		"LSB_INTEGER/16": func(p *synthProduct) {
			p.edit("SAMPLE_TYPE", "  SAMPLE_TYPE = LSB_INTEGER").edit("SAMPLE_BITS", "  SAMPLE_BITS = 16")
		},
		"UNSIGNED_INTEGER/8": func(p *synthProduct) {
			p.edit("SAMPLE_TYPE", "  SAMPLE_TYPE = UNSIGNED_INTEGER").edit("SAMPLE_BITS", "  SAMPLE_BITS = 8")
		},
		"PC_REAL/64":            func(p *synthProduct) { p.edit("SAMPLE_BITS", "  SAMPLE_BITS = 64") },
		"unknown SAMPLE_TYPE":   func(p *synthProduct) { p.edit("SAMPLE_TYPE", "  SAMPLE_TYPE = WEIRD_REAL") },
		"BANDS = 3":             func(p *synthProduct) { p.edit("BANDS", "  BANDS = 3") },
		"LINE_PREFIX_BYTES = 8": func(p *synthProduct) { p.appendLine("SAMPLE_BITS", "  LINE_PREFIX_BYTES = 8") },
		"RECORD_BYTES != W*4":   func(p *synthProduct) { p.edit("RECORD_BYTES", "RECORD_BYTES = 20") },
		"detached ^IMAGE":       func(p *synthProduct) { p.edit("^IMAGE", `^IMAGE = ("X.IMG", 1)`) },
		"SCALING_FACTOR 0.5":    func(p *synthProduct) { p.edit("SCALING_FACTOR", "  SCALING_FACTOR = 0.5") },
		"OFFSET 10":             func(p *synthProduct) { p.edit("OFFSET", "  OFFSET = 10.0") },
		"RECORD_TYPE STREAM":    func(p *synthProduct) { p.edit("RECORD_TYPE", "RECORD_TYPE = STREAM") },
		"decimal MISSING":       func(p *synthProduct) { p.edit("MISSING_CONSTANT", "  MISSING_CONSTANT = -3.4028226550889045e+38") },
		"other hex MISSING":     func(p *synthProduct) { p.edit("MISSING_CONSTANT", "  MISSING_CONSTANT = 16#FF7FFFFC#") },
		// Semantic checks.
		"DTER radius product":  func(p *synthProduct) { p.ProductID = "DTERD_TEST_A01" },
		"DTEP polar product":   func(p *synthProduct) { p.ProductID = "DTEPD_TEST_A01" },
		"PLANETOGRAPHIC":       func(p *synthProduct) { p.edit("COORDINATE_SYSTEM_NAME", "  COORDINATE_SYSTEM_NAME = PLANETOGRAPHIC") },
		"WEST longitude":       func(p *synthProduct) { p.edit("POSITIVE_LONGITUDE_DIRECTION", "  POSITIVE_LONGITUDE_DIRECTION = WEST") },
		"rotation 90":          func(p *synthProduct) { p.edit("MAP_PROJECTION_ROTATION", "  MAP_PROJECTION_ROTATION = 90.0 <DEG>") },
		"polar stereographic":  func(p *synthProduct) { p.edit("MAP_PROJECTION_TYPE", `  MAP_PROJECTION_TYPE = "POLAR STEREOGRAPHIC"`) },
		"no Mars 2000 in NOTE": func(p *synthProduct) { p.replaceText("(Mars\r\n                      2000 Datum)", "(MOLA radius)") },
		"wrong dataset":        func(p *synthProduct) { p.edit("DATA_SET_ID", `DATA_SET_ID = "MRO-M-HIRISE-3-RDR-V1.1"`) },
		"radius in metres":     func(p *synthProduct) { p.edit("A_AXIS_RADIUS", "  A_AXIS_RADIUS = 3394839.8 <M>") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := newSynthProduct(4, 4)
			mutate(p)
			expectInvalidProduct(t, p)
		})
	}
}

func TestTruncatedFileRejected(t *testing.T) {
	p := newSynthProduct(4, 4)
	p.truncate = 1
	expectInvalidProduct(t, p)
}

func TestPCRealDecoding(t *testing.T) {
	cases := []struct {
		raw  []byte
		want float32
	}{
		{[]byte{0x00, 0x00, 0x80, 0x3F}, 1.0},
		{[]byte{0x00, 0x00, 0x80, 0xBF}, -1.0},
	}
	for _, c := range cases {
		dst := make([]uint32, 1)
		decodePCReal(dst, c.raw)
		if got := math.Float32frombits(dst[0]); got != c.want {
			t.Errorf("% x -> %v want %v", c.raw, got, c.want)
		}
	}
	for _, v := range []float32{916.06, 1041.58} {
		raw := binary.LittleEndian.AppendUint32(nil, math.Float32bits(v))
		dst := make([]uint32, 1)
		decodePCReal(dst, raw)
		if math.Float32frombits(dst[0]) != v {
			t.Errorf("round trip of %v failed", v)
		}
		// A big-endian-written value decodes to a different number that the
		// A02 VALID range rejects.
		be := binary.BigEndian.AppendUint32(nil, math.Float32bits(v))
		decodePCReal(dst, be)
		pv := pixelValidity{missingBits: isisNull4Bits, minimum: 916.06, maximum: 1041.58}
		if pv.valid(dst[0]) {
			t.Errorf("big-endian %v decoded to valid %v", v, math.Float32frombits(dst[0]))
		}
	}
}

func TestMissingConstantMatchedByRawBits(t *testing.T) {
	lab, err := ParseLabel(readA02Label(t))
	if err != nil {
		t.Fatal(err)
	}
	if lab.MissingConstantBits != 0xFF7FFFFB {
		t.Fatalf("MissingConstantBits = %#x", lab.MissingConstantBits)
	}
	v := float64(math.Float32frombits(0xFF7FFFFB))
	if math.IsNaN(v) || math.IsInf(v, 0) {
		t.Fatal("0xFF7FFFFB is expected to be a finite float32 (that is why the bit check exists)")
	}
	// Widen VALID range so only the bit check can reject it.
	pv := pixelValidity{missingBits: lab.MissingConstantBits, minimum: -3.5e38, maximum: 3.5e38}
	if pv.valid(0xFF7FFFFB) {
		t.Fatal("MISSING_CONSTANT accepted with a widened VALID range")
	}
	if !pv.valid(math.Float32bits(-3.4e38)) {
		t.Fatal("ordinary large finite value rejected under widened range")
	}
}

func TestPixelValidity(t *testing.T) {
	pv := pixelValidity{missingBits: isisNull4Bits, minimum: 916.06, maximum: 1041.58}
	invalid := map[string]uint32{
		"quiet NaN +":        0x7FC00000,
		"quiet NaN -":        0xFFC00000,
		"signalling NaN +":   0x7F800001,
		"signalling NaN -":   0xFF800001,
		"+Inf":               0x7F800000,
		"-Inf":               0xFF800000,
		"below VALID_MIN":    math.Float32bits(math.Nextafter32(916.06, 0)),
		"above VALID_MAX":    math.Float32bits(math.Nextafter32(1041.58, 2000)),
		"zero (below range)": 0,
	}
	for b := uint32(0xFF7FFFFA); b <= 0xFF7FFFFF; b++ {
		invalid[fmt.Sprintf("ISIS special %#x", b)] = b
	}
	for name, bits := range invalid {
		if pv.valid(bits) {
			t.Errorf("%s (%#x) accepted", name, bits)
		}
	}
	// The VALID bounds compare as float64 against the label's decimal values,
	// so the bound itself is valid only if float32(bound) lies inside.
	for _, v := range []float32{1000, 1041.5} {
		if !pv.valid(math.Float32bits(v)) {
			t.Errorf("%v rejected", v)
		}
	}
	exact := pixelValidity{missingBits: isisNull4Bits, minimum: 916.0, maximum: 1041.5}
	if !exact.valid(math.Float32bits(916.0)) || !exact.valid(math.Float32bits(1041.5)) {
		t.Error("VALID_MINIMUM/VALID_MAXIMUM themselves must be valid")
	}
}

func TestReadWindowBounds(t *testing.T) {
	prod := newSynthProduct(5, 4).open(t)
	if _, err := prod.ReadWindow(3, 0, 3, 1); !errors.Is(err, ErrNoCoverage) {
		t.Fatalf("window past east edge: %v", err)
	}
	w, err := prod.ReadWindow(1, 1, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, v := w.At(4, 3); v != 3004 {
		t.Fatalf("SE pixel = %v", v)
	}
}

// TestImagePointerRejected: every ^IMAGE value that would put the image over
// the label, before the file, or past int64 range is rejected, never decoded.
func TestImagePointerRejected(t *testing.T) {
	for _, ptr := range []string{
		"1",           // record 1: the image would start at byte 0, over the label
		"1 <BYTES>",   // byte 1: same
		"100 <BYTES>", // inside the label text
		"0", "-3",     // not positive
		"9223372036854775807",     // (n-1)·RECORD_BYTES overflows int64 (was offset -32000)
		"2305843009213693953",     // overflow wraps to offset 0 (was accepted)
		"281474976710658 <BYTES>", // beyond the 2^48 offset bound
		"2 <RECORDS>",             // unit not supported
		"2.0",                     // not an integer
	} {
		t.Run(ptr, func(t *testing.T) {
			p := newSynthProduct(4000, 3)
			p.imagePointer = func(rb, records int) (string, int) { return ptr, rb }
			expectInvalidProduct(t, p)
		})
	}
}

// TestImagePointerAtLabelEnd: an image right after the label (END + CRLF) is
// valid; the same file with the pointer moved to the last byte of "END" is not.
func TestImagePointerAtLabelEnd(t *testing.T) {
	probe := newSynthProduct(4000, 3)
	probe.imagePointer = func(rb, records int) (string, int) { return "4001 <BYTES>", 4000 }
	end := probe.open(t).Label.LabelBytes

	p := newSynthProduct(4000, 3)
	start := end + 2 // after END and its CRLF
	p.imagePointer = func(rb, records int) (string, int) { return fmt.Sprintf("%d <BYTES>", start+1), start }
	good := p.bytes()
	if _, err := OpenProduct(bytes.NewReader(good), int64(len(good))); err != nil {
		t.Fatalf("image right after the label (byte %d) rejected: %v", start, err)
	}
	// "end <BYTES>" is 1-based, so the image would start at byte end-1, the "D" of END.
	from, to := fmt.Sprintf("= %d <BYTES>", start+1), fmt.Sprintf("= %d <BYTES>", end)
	if len(from) != len(to) {
		t.Skip("pointer width changes; boundary not representable")
	}
	bad := bytes.Replace(good, []byte(from), []byte(to), 1)
	if bytes.Equal(bad, good) {
		t.Fatal("pointer replacement did not apply")
	}
	if _, err := OpenProduct(bytes.NewReader(bad), int64(len(bad))); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("image starting inside END accepted: %v", err)
	}
}

func TestRasterSideLimit(t *testing.T) {
	p := newSynthProduct(4, 4)
	p.edit("LINES", fmt.Sprintf("  LINES = %d", 1<<20+1))
	expectInvalidProduct(t, p)
}
