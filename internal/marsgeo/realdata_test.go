package marsgeo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// Real HiRISE data tests. They always run: the committed crop
// (testdata/hirise, 0.64 MB) is a 400 x 400 px window of the real A02 DTM,
// fetched with marsgeo-prep, with provenance alongside.

const (
	cropFile       = "DTEED_029716_1980_029426_1980_A02_crop.IMG"
	a02ProductID   = "DTEED_029716_1980_029426_1980_A02"
	a02SourceSHA   = "3e40595b71434951188f683e9818e5ee2e818b885d7908d5f9b68b659dc02d8a"
	cropX0, cropY0 = 1552, 3926
	goldenSeed     = 2026
)

var hiriseDir = filepath.Join("testdata", "hirise")

type cropProvenance struct {
	ProductID      string `json:"productId"`
	SourceSHA256   string `json:"sourceSha256"`
	SourceLabelSHA string `json:"sourceLabelSha256"`
	CropWindow     [4]int `json:"cropWindow"`
	CropFile       string `json:"cropFile"`
	CropBytes      int    `json:"cropBytes"`
	CropSHA256     string `json:"cropSha256"`
	Credit         string `json:"credit"`
	UsagePolicy    string `json:"usagePolicy"`
}

func openCropTerrain(t *testing.T) *Terrain {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(hiriseDir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := LoadRegistry(raw)
	if err != nil {
		t.Fatal(err)
	}
	terr, err := reg.Open(hiriseDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { terr.Close() })
	return terr
}

func TestRealCropProvenance(t *testing.T) {
	var prov cropProvenance
	b, err := os.ReadFile(filepath.Join(hiriseDir, a02ProductID+"_crop.provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &prov); err != nil {
		t.Fatal(err)
	}
	crop, err := os.ReadFile(filepath.Join(hiriseDir, cropFile))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(crop)
	label := sha256.Sum256(readA02Label(t))
	switch {
	case len(crop) > 2<<20:
		t.Errorf("crop is %d bytes, over the 2 MB cap", len(crop))
	case prov.CropSHA256 != hex.EncodeToString(sum[:]) || prov.CropBytes != len(crop) || prov.CropFile != cropFile:
		t.Errorf("crop checksum/size do not match provenance")
	case prov.ProductID != a02ProductID || prov.SourceSHA256 != a02SourceSHA || prov.SourceLabelSHA != hex.EncodeToString(label[:]):
		t.Errorf("source identity does not match the A02 product: %+v", prov)
	case prov.CropWindow != [4]int{cropX0, cropY0, 400, 400}:
		t.Errorf("crop window %v", prov.CropWindow)
	case prov.Credit != "NASA/JPL/University of Arizona" || prov.UsagePolicy == "":
		t.Errorf("attribution missing")
	}
}

func TestRealCropIsFullyValidAndMatchesGDAL(t *testing.T) {
	crop, err := os.ReadFile(filepath.Join(hiriseDir, cropFile))
	if err != nil {
		t.Fatal(err)
	}
	prod, err := OpenProduct(bytes.NewReader(crop), int64(len(crop)))
	if err != nil {
		t.Fatal(err)
	}
	win, _ := prod.ReadWindow(0, 0, 400, 400)
	if n := invalidInWindow(win, Rect{0, 0, 399, 399}, prod.validity()); n != 0 {
		t.Fatalf("%d invalid pixels in the committed crop", n)
	}
	// Every GDAL reference point inside the crop: same source pixel, same
	// float32 bits, and the crop's own georef finds the same pixel.
	ref := loadGDALReference(t)
	checked := 0
	for _, l := range ref.Locations {
		cx, cy := l.ReportedPixel-cropX0, l.ReportedLine-cropY0
		if cx < 0 || cy < 0 || cx >= 400 || cy >= 400 {
			continue
		}
		bits, _ := win.At(cx, cy)
		gv, _ := strconv.ParseFloat(l.Value, 64)
		if math.Float32bits(float32(gv)) != bits {
			t.Errorf("crop pixel (%d,%d) = %#08x, GDAL says %s", cx, cy, bits, l.Value)
		}
		x, y := prod.geo.toPixel(Coordinate{Latitude: l.Lat, LongitudeEast: normalizeLongitude(l.Lon)})
		if int(math.Floor(x+0.5)) != cx || int(math.Floor(y+0.5)) != cy {
			t.Errorf("crop georef puts GDAL point at (%v,%v), want (%d,%d)", x, y, cx, cy)
		}
		checked++
	}
	if checked < 13 {
		t.Fatalf("only %d GDAL points inside the crop", checked)
	}
}

func TestRealBuildFromCrop(t *testing.T) {
	terr := openCropTerrain(t)
	lab := terr.products[0].Label
	for _, seed := range []uint32{0, 1, goldenSeed, 4294967295} {
		d, err := terr.Build(seed)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		mustValidPayload(t, &d)
		if d.Version != 1 || d.Grid != DefaultGrid || d.Elevation.Columns != 51 || d.Elevation.Rows != 51 || len(d.Elevation.SamplesMeters) != 2601 {
			t.Fatalf("seed %d: shape %+v", seed, d.Grid)
		}
		if d.Elevation.Datum != "mars-2000-areoid" || d.Elevation.Sampling != "cell-corners" || d.Seed != seed {
			t.Fatalf("seed %d: metadata", seed)
		}
		if d.Source.ProductID == nil || *d.Source.ProductID != a02ProductID || d.Source.DatasetID != HiRISEDTMDatasetID ||
			d.Source.Provider != HiRISEProvider || d.Source.ResolutionMeters != 2.0195488956581 {
			t.Fatalf("seed %d: source %+v", seed, d.Source)
		}
		for i, v := range d.Elevation.SamplesMeters {
			if math.IsNaN(v) || v < lab.ValidMinimum || v > lab.ValidMaximum {
				t.Fatalf("seed %d sample %d = %v outside [%v, %v]", seed, i, v, lab.ValidMinimum, lab.ValidMaximum)
			}
		}
		if d.Center.Latitude < 17.68 || d.Center.Latitude > 17.97 || d.Center.LongitudeEast < 232.42 || d.Center.LongitudeEast > 232.55 {
			t.Fatalf("seed %d: centre %+v outside the A02 footprint", seed, d.Center)
		}
	}
}

// independentCorner recomputes one corner from raw pixels with its own
// arithmetic (no DTMSource): the contract §3 placement on R = 3,389,500 m,
// the product's equirectangular projection on its own radius, strict
// bilinear, 5 x 5 mean.
func independentCorner(t *testing.T, prod *Product, center Coordinate, col, row int) float64 {
	l := prod.Label
	win, _ := prod.ReadWindow(0, 0, l.Samples, l.Lines)
	rad := math.Pi / 180
	var sum float64
	for j := 0; j < 5; j++ {
		for i := 0; i < 5; i++ {
			east := float64(col-25)*10 + float64(2*i-4)
			north := float64(25-row)*10 - float64(2*j-4)
			lat := center.Latitude + north/(rad*3389500)
			lon := center.LongitudeEast + east/(rad*3389500*math.Cos(center.Latitude*rad))
			x := l.SampleOffset + (lon-l.CenterLongitude)*rad*l.RadiusMeters*math.Cos(l.CenterLatitude*rad)/l.MapScale
			y := l.LineOffset - lat*rad*l.RadiusMeters/l.MapScale
			x0, y0 := math.Floor(x), math.Floor(y)
			fx, fy := x-x0, y-y0
			px := func(dx, dy int) float64 { _, v := win.At(int(x0)+dx, int(y0)+dy); return float64(v) }
			sum += (1-fy)*((1-fx)*px(0, 0)+fx*px(1, 0)) + fy*((1-fx)*px(0, 1)+fx*px(1, 1))
		}
	}
	return sum / 25
}

func TestRealCornersMatchIndependentComputation(t *testing.T) {
	terr := openCropTerrain(t)
	d, err := terr.Build(goldenSeed)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]int{{0, 0}, {50, 0}, {0, 50}, {50, 50}, {25, 25}, {17, 33}} {
		got := d.Elevation.SamplesMeters[c[1]*51+c[0]]
		if want := independentCorner(t, terr.products[0], d.Center, c[0], c[1]); math.Abs(got-want) > 1e-9 {
			t.Errorf("corner %v = %.12f, independent %.12f", c, got, want)
		}
	}
}

// Golden values, pinned last (after the P9 GDAL check). The value table is
// checked on every architecture within 1e-6 m; the byte hash only on amd64,
// since math.Cos may be FMA-fused elsewhere (docs/MARSGEO.md §4).
const goldenPayloadSHA256AMD64 = "ab33dac3273c44aa9a0510bcb8dcb8d0edd5b0aee09dd9bd6e92dca194671a0d"

func TestRealGoldenPayload(t *testing.T) {
	terr := openCropTerrain(t)
	d, err := terr.Build(goldenSeed)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(&d)
	path := filepath.Join(hiriseDir, "golden_seed_2026.json")
	if os.Getenv("MARSGEO_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(got)
		t.Logf("wrote %s; payload sha256 %s (GOARCH %s)", path, hex.EncodeToString(sum[:]), runtime.GOARCH)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want MarsTerrainDataV1
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if want.Center != d.Center || want.Seed != d.Seed || len(want.Elevation.SamplesMeters) != 2601 {
		t.Fatalf("golden location differs: %+v vs %+v", want.Center, d.Center)
	}
	for i, w := range want.Elevation.SamplesMeters {
		if math.Abs(d.Elevation.SamplesMeters[i]-w) > 1e-6 {
			t.Fatalf("sample %d = %v, golden %v", i, d.Elevation.SamplesMeters[i], w)
		}
	}
	if runtime.GOARCH != "amd64" {
		t.Skipf("payload byte hash is pinned for amd64 only (GOARCH %s); values matched within 1e-6 m", runtime.GOARCH)
	}
	sum := sha256.Sum256(got)
	if h := hex.EncodeToString(sum[:]); h != goldenPayloadSHA256AMD64 {
		t.Fatalf("payload sha256 %s, golden %s", h, goldenPayloadSHA256AMD64)
	}
}

func TestProductionRegistry(t *testing.T) {
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Entries) != 1 {
		t.Fatalf("%d entries", len(reg.Entries))
	}
	e := reg.Entries[0]
	if e.ProductID != a02ProductID || e.SHA256 != a02SourceSHA || e.ByteSize != 115674048 || e.Kernel != 5 || e.ValidCentres.Step != 25 {
		t.Fatalf("entry %+v", e)
	}
	if n := e.ValidCentres.Count(); n != 28546 {
		t.Fatalf("valid centres %d, expected 28546", n)
	}
	// The crop's lattice centres, mapped back to source pixels, are a subset
	// of the production lattice (both are scans of the same pixels).
	crop := openCropTerrain(t).reg.Entries[0].ValidCentres
	prodSet := map[[2]int]bool{}
	for n := 0; n < e.ValidCentres.Count(); n++ {
		x, y, _ := e.ValidCentres.Pixel(n)
		prodSet[[2]int{x, y}] = true
	}
	for n := 0; n < crop.Count(); n++ {
		x, y, _ := crop.Pixel(n)
		if (x+cropX0)%25 == 0 && (y+cropY0)%25 == 0 && !prodSet[[2]int{x + cropX0, y + cropY0}] {
			t.Errorf("crop centre (%d,%d) not valid in the production lattice", x+cropX0, y+cropY0)
		}
	}
}

func TestProductionRegistryWithFullProduct(t *testing.T) {
	dir := os.Getenv("MARSGEO_DATA_DIR")
	if dir == "" {
		t.Skip("MARSGEO_DATA_DIR not set")
	}
	reg, _ := DefaultRegistry()
	terr, err := reg.Open(dir)
	if err != nil {
		t.Fatalf("MARSGEO_DATA_DIR is set but the production registry does not open: %v", err)
	}
	defer terr.Close()
	for _, seed := range []uint32{0, 1, 7, goldenSeed, 4294967295} {
		d, err := terr.Build(seed)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		mustValidPayload(t, &d)
	}
}
