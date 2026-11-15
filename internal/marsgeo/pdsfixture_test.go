package marsgeo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
)

// synthProduct builds synthetic HiRISE-like PDS3 products in memory. Its
// label mirrors the real A02 label's keywords; edit() rewrites single lines
// so each test changes exactly one thing.
type synthProduct struct {
	W, H                     int
	MapScale                 float64
	SampleOffset, LineOffset float64
	CenterLat, CenterLon     float64
	RadiusKm                 float64
	ValidMin, ValidMax       float64
	ProductID                string
	edits                    []func(string) string
	imagePointer             func(recordBytes, labelRecords int) (ptr string, offset int)
	truncate                 int
	Bits                     []uint32
}

func newSynthProduct(w, h int) *synthProduct {
	p := &synthProduct{
		W: w, H: h, MapScale: 2, SampleOffset: 100.5, LineOffset: 900000.5,
		CenterLat: 15, CenterLon: 232.49, RadiusKm: 3394.8398133163,
		ValidMin: -9000, ValidMax: 9000, ProductID: "DTEED_TEST_A01",
		Bits: make([]uint32, w*h),
	}
	for i := range p.Bits {
		p.Bits[i] = math.Float32bits(float32(1000*(i/w) + i%w)) // value = 1000·row + col
	}
	return p
}

func (p *synthProduct) set(x, y int, bits uint32) { p.Bits[y*p.W+x] = bits }

// edit replaces the first line whose keyword is key (indentation ignored).
// An empty replacement deletes the line.
func (p *synthProduct) edit(key, line string) *synthProduct {
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=.*\r?\n`)
	p.edits = append(p.edits, func(s string) string {
		loc := re.FindStringIndex(s)
		if loc == nil {
			panic("synthProduct.edit: no line for " + key)
		}
		if line != "" {
			line += "\r\n"
		}
		return s[:loc[0]] + line + s[loc[1]:]
	})
	return p
}

// replaceText replaces literal label text (CRLF line endings).
func (p *synthProduct) replaceText(old, new string) *synthProduct {
	p.edits = append(p.edits, func(s string) string {
		if !strings.Contains(s, old) {
			panic("synthProduct.replaceText: text not found: " + old)
		}
		return strings.Replace(s, old, new, 1)
	})
	return p
}

// appendLine inserts a line just before the line with keyword before.
func (p *synthProduct) appendLine(before, line string) *synthProduct {
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(before) + `[ \t]*=`)
	p.edits = append(p.edits, func(s string) string {
		loc := re.FindStringIndex(s)
		return s[:loc[0]] + line + "\r\n" + s[loc[0]:]
	})
	return p
}

const synthLabelTemplate = `PDS_VERSION_ID            = PDS3
/* File format and length */
RECORD_TYPE               = FIXED_LENGTH
RECORD_BYTES              = %d
^IMAGE                    = %s
DATA_SET_ID               = "MRO-M-HIRISE-5-DTM-V1.0"
PRODUCT_ID                = "%s"
PRODUCT_VERSION_ID        = "1"
SOFTWARE_NAME             = "synthetic test"
OBJECT = IMAGE
  LINES            = %d
  LINE_SAMPLES     = %d
  BANDS            = 1
  OFFSET           = 0.0
  SCALING_FACTOR   = 1.0
  NOTE             = "Synthetic test raster. Elevations in meters above the
                      martian equipotential surface (Mars
                      2000 Datum)."
  SAMPLE_BITS      = 32
  SAMPLE_TYPE      = PC_REAL
  MISSING_CONSTANT = 16#FF7FFFFB#
  VALID_MINIMUM    = %s
  VALID_MAXIMUM    = %s
END_OBJECT = IMAGE
OBJECT = IMAGE_MAP_PROJECTION
  MAP_PROJECTION_TYPE          = "EQUIRECTANGULAR"
  A_AXIS_RADIUS                = %s <KM>
  B_AXIS_RADIUS                = %s <KM>
  C_AXIS_RADIUS                = %s <KM>
  COORDINATE_SYSTEM_NAME       = PLANETOCENTRIC
  POSITIVE_LONGITUDE_DIRECTION = EAST
  CENTER_LATITUDE              = %s <DEG>
  CENTER_LONGITUDE             = %s <DEG>
  MAP_PROJECTION_ROTATION      = 0.0 <DEG>
  MAP_SCALE                    = %s <METERS/PIXEL>
  MAXIMUM_LATITUDE             = 18.0 <DEG>
  MINIMUM_LATITUDE             = 17.0 <DEG>
  LINE_PROJECTION_OFFSET       = %s <PIXEL>
  SAMPLE_PROJECTION_OFFSET     = %s <PIXEL>
  EASTERNMOST_LONGITUDE        = 232.6 <DEG>
  WESTERNMOST_LONGITUDE        = 232.4 <DEG>
END_OBJECT = IMAGE_MAP_PROJECTION
END
`

func ff(v float64) string { return fmt.Sprintf("%.17g", v) }

func (p *synthProduct) label(ptr string) string {
	s := fmt.Sprintf(synthLabelTemplate, p.W*4, ptr, p.ProductID, p.H, p.W,
		ff(p.ValidMin), ff(p.ValidMax), ff(p.RadiusKm), ff(p.RadiusKm), ff(p.RadiusKm),
		ff(p.CenterLat), ff(p.CenterLon), ff(p.MapScale), ff(p.LineOffset), ff(p.SampleOffset))
	s = strings.ReplaceAll(s, "\n", "\r\n")
	for _, e := range p.edits {
		s = e(s)
	}
	return s
}

// bytes renders label + raster as a PDS3 file.
func (p *synthProduct) bytes() []byte {
	rb := p.W * 4
	labelLen := len(p.label("99999999"))
	records := (labelLen + rb - 1) / rb
	ptr, offset := fmt.Sprint(records+1), records*rb
	if p.imagePointer != nil {
		ptr, offset = p.imagePointer(rb, records)
	}
	var buf bytes.Buffer
	lab := p.label(ptr)
	buf.WriteString(lab)
	for buf.Len() < offset {
		buf.WriteByte(' ')
	}
	if buf.Len() != offset {
		panic("label longer than image offset")
	}
	raw := make([]byte, 4)
	for _, b := range p.Bits {
		binary.LittleEndian.PutUint32(raw, b)
		buf.Write(raw)
	}
	out := buf.Bytes()
	return out[:len(out)-p.truncate]
}

func (p *synthProduct) open(t testing.TB) *Product {
	t.Helper()
	b := p.bytes()
	prod, err := OpenProduct(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("open synthetic product: %v", err)
	}
	return prod
}
