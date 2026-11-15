package marsgeo

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Tests in this file need the full 115 MB A02 product, which stays outside
// git. They run when MARSGEO_DATA_DIR is set (and then fail, not skip, if the
// product is missing). Fetch it with cmd/marsgeo-prep (docs/MARSGEO.md).

const a02File = "DTEED_029716_1980_029426_1980_A02.IMG"

func openFullA02(t *testing.T) *Product {
	t.Helper()
	dir := os.Getenv("MARSGEO_DATA_DIR")
	if dir == "" {
		t.Skip("MARSGEO_DATA_DIR not set; full-product tests skipped")
	}
	f, err := os.Open(filepath.Join(dir, a02File))
	if err != nil {
		t.Fatalf("MARSGEO_DATA_DIR is set but the A02 product is missing: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	st, _ := f.Stat()
	prod, err := OpenProduct(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, prod.Label.ImageOffset)
	f.ReadAt(head, 0)
	sum := sha256.Sum256(head)
	if hex.EncodeToString(sum[:]) != loadGDALReference(t).LabelSha256 {
		t.Fatal("full product label differs from the committed .LBL")
	}
	return prod
}

// TestFullA02MatchesGDALValues is the final real-product verification: at
// every gdallocationinfo point, our georef picks the same pixel and our
// reader returns the same float32, bit for bit.
func TestFullA02MatchesGDALValues(t *testing.T) {
	prod := openFullA02(t)
	ref := loadGDALReference(t)
	nodata := 0
	for _, l := range ref.Locations {
		x, y := prod.geo.toPixel(Coordinate{Latitude: l.Lat, LongitudeEast: normalizeLongitude(l.Lon)})
		px, py := int(math.Floor(x+0.5)), int(math.Floor(y+0.5))
		if px != l.ReportedPixel || py != l.ReportedLine {
			t.Fatalf("pixel (%d,%d) vs GDAL (%d,%d)", px, py, l.ReportedPixel, l.ReportedLine)
		}
		w, err := prod.ReadWindow(px, py, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		bits, v := w.At(px, py)
		gv, err := strconv.ParseFloat(l.Value, 64)
		if err != nil {
			t.Fatal(err)
		}
		if bits == isisNull4Bits {
			nodata++
			// GDAL prints NoData with 14 significant digits; compare as float32.
			if float32(gv) != v {
				t.Errorf("NoData at (%d,%d): GDAL %s, ours %v", px, py, l.Value, v)
			}
			continue
		}
		// GDAL prints 15 significant digits, which identify a float32 uniquely.
		if math.Float32bits(float32(gv)) != bits {
			t.Errorf("value at (%d,%d): GDAL %s, ours %v (%#08x)", px, py, l.Value, v, bits)
		}
	}
	if nodata != 4 {
		t.Errorf("expected the 4 raster-corner probes to be MISSING_CONSTANT, got %d", nodata)
	}
}

// TestFullA02PixelStatistics records how the validity predicate classifies
// the whole product, including pixels just outside the label's decimal
// VALID range.
func TestFullA02PixelStatistics(t *testing.T) {
	prod := openFullA02(t)
	pv := prod.validity()
	var valid, missing, special, nonFinite, belowMin, aboveMax int
	minV, maxV := math.Inf(1), math.Inf(-1)
	for y := 0; y < prod.Label.Lines; y++ {
		w, err := prod.ReadWindow(0, y, prod.Label.Samples, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range w.Bits {
			v := float64(math.Float32frombits(b))
			switch {
			case b == isisNull4Bits:
				missing++
			case isISISSpecialBits(b):
				special++
			case math.IsNaN(v) || math.IsInf(v, 0):
				nonFinite++
			case v < pv.minimum:
				belowMin++
				minV = math.Min(minV, v)
			case v > pv.maximum:
				aboveMax++
				maxV = math.Max(maxV, v)
			default:
				valid++
				minV, maxV = math.Min(minV, v), math.Max(maxV, v)
			}
		}
	}
	t.Logf("valid %d, MISSING_CONSTANT %d, other ISIS special %d, non-finite %d, below VALID_MINIMUM %d, above VALID_MAXIMUM %d; finite range %.9g..%.9g (label %v..%v)",
		valid, missing, special, nonFinite, belowMin, aboveMax, minV, maxV, prod.Label.ValidMinimum, prod.Label.ValidMaximum)
	if valid+missing+special+nonFinite+belowMin+aboveMax != prod.Label.Lines*prod.Label.Samples {
		t.Fatal("pixel count mismatch")
	}
	// Observed 2026-11-15: one genuine pixel (916.057068 m) lies below the
	// label's VALID_MINIMUM 916.06, which is rounded to two decimals. The
	// predicate treats it as invalid (conservative: it can only reduce
	// coverage). Pinned so a change in the data or the rule is noticed.
	if belowMin != 1 || aboveMax != 0 {
		t.Errorf("pixels outside the label VALID range: %d below, %d above; expected 1 and 0", belowMin, aboveMax)
	}
	if special != 0 || nonFinite != 0 {
		t.Errorf("unexpected special/non-finite pixels: %d / %d", special, nonFinite)
	}
}
