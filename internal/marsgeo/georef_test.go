package marsgeo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// --- Synthetic product: pixel centres, boundaries, orientation, offsets ---

func TestGeorefPixelCentreRoundTrip(t *testing.T) {
	p := newSynthProduct(40, 30).open(t)
	for _, px := range [][2]float64{{0, 0}, {39, 0}, {0, 29}, {39, 29}, {17, 11}, {-0.5, -0.5}, {39.5, 29.5}} {
		geo := p.geo.toGeographic(px[0], px[1])
		x, y := p.geo.toPixel(geo)
		if math.Abs(x-px[0]) > 1e-8 || math.Abs(y-px[1]) > 1e-8 { // ~2e-8 m; float64 resolves ~1e-9 px at 232°
			t.Errorf("pixel %v -> %+v -> (%v, %v)", px, geo, x, y)
		}
	}
}

func TestGeorefOrientation(t *testing.T) {
	p := newSynthProduct(40, 30).open(t)
	nw, se := p.geo.toGeographic(0, 0), p.geo.toGeographic(39, 29)
	if !(nw.Latitude > se.Latitude) || !(longitudeDelta(nw.LongitudeEast, se.LongitudeEast) > 0) {
		t.Fatalf("pixel (0,0) must be north-west of (39,29): %+v %+v", nw, se)
	}
	x1, y1 := p.geo.toPixel(Coordinate{Latitude: nw.Latitude, LongitudeEast: nw.LongitudeEast + 1e-4})
	x2, y2 := p.geo.toPixel(Coordinate{Latitude: nw.Latitude - 1e-4, LongitudeEast: nw.LongitudeEast})
	if !(x1 > 0 && math.Abs(y1) < 1e-8) || !(y2 > 0 && math.Abs(x2) < 1e-8) {
		t.Fatalf("x must grow east and y south: (%v,%v) (%v,%v)", x1, y1, x2, y2)
	}
}

// pointSource samples the ramp product (value = 1000·row + col) with plain
// strict bilinear at one geographic point.
func pointSample(t *testing.T, p *Product, geo Coordinate) (float64, error) {
	t.Helper()
	win, err := p.ReadWindow(0, 0, p.Label.Samples, p.Label.Lines)
	if err != nil {
		t.Fatal(err)
	}
	s := &DTMSource{prod: p, win: win, validity: p.validity()}
	x, y := p.geo.toPixel(geo)
	return s.bilinear(x, y)
}

func TestHalfAndOnePixelOffsets(t *testing.T) {
	p := newSynthProduct(40, 30).open(t)
	scale := p.Label.MapScale
	centre := p.geo.toGeographic(17, 11)
	check := func(name string, east, south, want float64) {
		t.Helper()
		local := LocalPoint{East: east * scale, North: -south * scale}
		// Move in the product's own projection, not the client's sphere.
		geo := Coordinate{
			Latitude:      centre.Latitude + float64(local.North/p.Label.RadiusMeters)/radians,
			LongitudeEast: centre.LongitudeEast + float64(local.East/(p.Label.RadiusMeters*math.Cos(p.Label.CenterLatitude*radians)))/radians,
		}
		v, err := pointSample(t, p, geo)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if math.Abs(v-want) > 1e-6 {
			t.Errorf("%s: %v want %v", name, v, want)
		}
	}
	check("centre", 0, 0, 11017)
	check("+0.5 px east", 0.5, 0, 11017.5)
	check("+1 px east", 1, 0, 11018)
	check("+0.5 px south", 0, 0.5, 11517)
	check("+1 px south", 0, 1, 12017)
}

func TestStrictBilinearEdges(t *testing.T) {
	p := newSynthProduct(10, 8).open(t)
	win, _ := p.ReadWindow(0, 0, 10, 8)
	s := &DTMSource{prod: p, win: win, validity: p.validity()}
	if _, err := s.bilinear(9-1e-9, 3); err != nil {
		t.Errorf("x = W-1-eps rejected: %v", err)
	}
	for _, xy := range [][2]float64{{9, 3}, {-1e-9, 3}, {3, 7}, {3, -0.1}} {
		if _, err := s.bilinear(xy[0], xy[1]); !errors.Is(err, ErrNoCoverage) {
			t.Errorf("stencil at %v accepted: %v", xy, err)
		}
	}
	// A zero-weight stencil pixel still has to be valid.
	pp := newSynthProduct(10, 8)
	pp.set(4, 3, isisNull4Bits)
	prod := pp.open(t)
	win, _ = prod.ReadWindow(0, 0, 10, 8)
	s = &DTMSource{prod: prod, win: win, validity: prod.validity()}
	if _, err := s.bilinear(3, 3); !errors.Is(err, ErrNoCoverage) {
		t.Errorf("zero-weight NoData stencil pixel accepted: %v", err)
	}
}

func TestBilinearPlaneAndGradient(t *testing.T) {
	p := newSynthProduct(6, 6).open(t)
	win, _ := p.ReadWindow(0, 0, 6, 6)
	s := &DTMSource{prod: p, win: win, validity: p.validity()}
	for _, xy := range [][2]float64{{1.25, 2.75}, {0.5, 0.5}, {3.9, 4.1}} {
		v, err := s.bilinear(xy[0], xy[1])
		if err != nil {
			t.Fatal(err)
		}
		if want := 1000*xy[1] + xy[0]; math.Abs(v-want) > 1e-9 {
			t.Errorf("plane at %v = %v want %v", xy, v, want)
		}
	}
	g := newSynthProduct(2, 2)
	for i, v := range []float32{1000, 1004, 1010, 1030} {
		g.Bits[i] = math.Float32bits(v)
	}
	gp := g.open(t)
	win, _ = gp.ReadWindow(0, 0, 2, 2)
	s = &DTMSource{prod: gp, win: win, validity: gp.validity()}
	// top = .75·1000 + .25·1004 = 1001; bottom = .75·1010 + .25·1030 = 1015; .5·1001 + .5·1015 = 1008
	v, err := s.bilinear(0.25, 0.5)
	if err != nil || v != 1008 {
		t.Fatalf("hand-computed bilinear = %v, %v; want 1008", v, err)
	}
}

func TestKernelSizeAndOffsets(t *testing.T) {
	for _, c := range []struct {
		scale float64
		want  int
	}{{2.0195488956581, 5}, {1.0, 10}, {1.00001, 10}, {0.99999, 11}, {2.5, 4}, {2.0, 5}} {
		if got := KernelSize(10, c.scale); got != c.want {
			t.Errorf("KernelSize(10, %v) = %d want %d", c.scale, got, c.want)
		}
		if k := KernelSize(10, c.scale); 10/float64(k) > c.scale {
			t.Errorf("spacing %v exceeds MAP_SCALE %v", 10/float64(k), c.scale)
		}
	}
	got := kernelOffsets(5, 10)
	for i, want := range []float64{-4, -2, 0, 2, 4} {
		if math.Abs(got[i]-want) > 1e-12 {
			t.Fatalf("offsets = %v", got)
		}
	}
}

func TestBoxAverage(t *testing.T) {
	// MAP_SCALE 2 m, k = 5: on the ramp, the mean of a plane equals the plane at the corner.
	p := newSynthProduct(60, 60)
	p.CenterLat, p.SampleOffset, p.LineOffset = 0, 30, 30
	p.CenterLon = 20
	prod := p.open(t)
	win, _ := prod.ReadWindow(0, 0, 60, 60)
	src, err := NewDTMSource(prod, win, 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	centre := prod.geo.toGeographic(30, 30)
	v, err := src.SampleCorner(centre, LocalPoint{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	// The client sphere is smaller than the product sphere, so the corner sits
	// at the pixel (30, 30) only to within the radius ratio; compare against
	// the plane evaluated at the corner's own pixel position.
	x, y := prod.geo.toPixel(centre)
	if want := 1000*y + x; math.Abs(v-want) > 1e-6 {
		t.Fatalf("box mean of a plane = %v want %v", v, want)
	}
	if _, err := NewDTMSource(prod, win, 4, 10); err == nil {
		t.Fatal("wrong kernel accepted")
	}
	// Determinism: identical bits on repeat.
	v2, _ := src.SampleCorner(centre, LocalPoint{East: 3, North: -7}, 10)
	v3, _ := src.SampleCorner(centre, LocalPoint{East: 3, North: -7}, 10)
	if math.Float64bits(v2) != math.Float64bits(v3) {
		t.Fatal("not deterministic")
	}
}

func TestBoxAverageHandComputed(t *testing.T) {
	// A checkerboard of 1000/1001 on a 1 m product at the equator, with the
	// product sphere equal to the client sphere so sub-sample positions are
	// exact. k = 10; offsets ±0.5..±4.5 m land halfway between pixel centres,
	// so every bilinear sub-sample is 1000.5 and so is the mean.
	p := newSynthProduct(30, 30)
	p.MapScale, p.CenterLat, p.CenterLon, p.SampleOffset, p.LineOffset = 1, 0, 0, 15, 15
	p.RadiusKm = MarsMeanRadiusMeters / 1000.0
	for i := range p.Bits {
		p.Bits[i] = math.Float32bits(float32(1000 + (i/30+i%30)%2))
	}
	p.ValidMin, p.ValidMax = 900, 1100
	prod := p.open(t)
	win, _ := prod.ReadWindow(0, 0, 30, 30)
	src, err := NewDTMSource(prod, win, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	v, err := src.SampleCorner(Coordinate{}, LocalPoint{}, 10)
	if err != nil || math.Abs(v-1000.5) > 1e-9 {
		t.Fatalf("checkerboard mean = %v, %v", v, err)
	}
}

// --- Real A02 label against the independent GDAL reference ---

type gdalReference struct {
	LabelSha256  string     `json:"labelSha256"`
	GDALVersion  string     `json:"gdalVersion"`
	Size         [2]int     `json:"size"`
	GeoTransform [6]float64 `json:"geoTransform"`
	NoDataValue  float64    `json:"noDataValue"`
	SRSWkt       string     `json:"srsWkt"`
	Transforms   []struct {
		Kind      string  `json:"kind"`
		GDALPixel float64 `json:"gdalPixel"`
		GDALLine  float64 `json:"gdalLine"`
		Lon       float64 `json:"lon"`
		Lat       float64 `json:"lat"`
	} `json:"transforms"`
	Locations []struct {
		Pixel, Line                 int
		DPixel                      float64 `json:"dPixel"`
		DLine                       float64 `json:"dLine"`
		Lon                         float64 `json:"lon"`
		Lat                         float64 `json:"lat"`
		ReportedPixel, ReportedLine int
		Value                       string `json:"value"`
	} `json:"locations"`
}

func loadGDALReference(t testing.TB) *gdalReference {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hirise", "gdal_reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ref gdalReference
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatal(err)
	}
	if len(ref.Transforms) == 0 || len(ref.Locations) == 0 {
		t.Fatal("empty GDAL reference")
	}
	return &ref
}

func a02Georef(t testing.TB) (*Label, georef) {
	t.Helper()
	lab, err := ParseLabel(readA02Label(t))
	if err != nil {
		t.Fatal(err)
	}
	return lab, newGeoref(lab)
}

// gdalMismatches counts reference points our georef disagrees with: GDAL
// pixel/line (corner-based) p maps to our pixel p - 0.5.
func gdalMismatches(t *testing.T, g georef, ref *gdalReference, report bool) int {
	bad := 0
	for _, tr := range ref.Transforms {
		geo := g.toGeographic(tr.GDALPixel-0.5, tr.GDALLine-0.5)
		if d := math.Max(math.Abs(geo.Latitude-tr.Lat), math.Abs(longitudeDelta(tr.Lon, geo.LongitudeEast))); d > 1e-9 {
			bad++
			if report {
				t.Errorf("%s GDAL (%v, %v): ours %+v, GDAL (%v, %v), diff %.3g deg", tr.Kind, tr.GDALPixel, tr.GDALLine, geo, tr.Lon, tr.Lat, d)
			}
		}
	}
	for _, l := range ref.Locations {
		x, y := g.toPixel(Coordinate{Latitude: l.Lat, LongitudeEast: normalizeLongitude(l.Lon)})
		px, py := int(math.Floor(x+0.5)), int(math.Floor(y+0.5))
		if px != l.ReportedPixel || py != l.ReportedLine {
			bad++
			if report {
				t.Errorf("location %d,%d %+v/%+v: ours (%d, %d), GDAL (%d, %d)", l.Pixel, l.Line, l.DPixel, l.DLine, px, py, l.ReportedPixel, l.ReportedLine)
			}
		}
	}
	return bad
}

func TestGeorefMatchesGDALReference(t *testing.T) {
	ref := loadGDALReference(t)
	sum := sha256.Sum256(readA02Label(t))
	if hex.EncodeToString(sum[:]) != ref.LabelSha256 {
		t.Fatal("committed A02 label differs from the one GDAL was run against")
	}
	lab, g := a02Georef(t)
	if ref.Size != [2]int{lab.Samples, lab.Lines} {
		t.Fatalf("size %v vs label %dx%d", ref.Size, lab.Samples, lab.Lines)
	}
	// Our outer NW corner (-0.5, -0.5) in projected metres equals GDAL's origin.
	gt := ref.GeoTransform
	ulx := (-0.5 + g.shift - lab.SampleOffset) * lab.MapScale
	uly := (lab.LineOffset + 0.5 - g.shift) * lab.MapScale
	if math.Abs(ulx-gt[0]) > 1e-6 || math.Abs(uly-gt[3]) > 1e-6 {
		t.Errorf("origin (%v, %v) vs GDAL (%v, %v)", ulx, uly, gt[0], gt[3])
	}
	if gt[1] != lab.MapScale || gt[5] != -lab.MapScale || gt[2] != 0 || gt[4] != 0 {
		t.Errorf("geotransform %v is not north-up with MAP_SCALE %v", gt, lab.MapScale)
	}
	// GDAL's SRS uses the label's sphere, standard parallel and meridian.
	for name, want := range map[string]float64{
		`ELLIPSOID\["[^"]*",([0-9.]+)`:                  lab.RadiusMeters,
		`"Latitude of 1st standard parallel",([0-9.]+)`: lab.CenterLatitude,
		`"Longitude of natural origin",([0-9.]+)`:       lab.CenterLongitude,
	} {
		m := regexp.MustCompile(name).FindStringSubmatch(ref.SRSWkt)
		if m == nil {
			t.Fatalf("SRS WKT has no %s", name)
		}
		if got, _ := strconv.ParseFloat(m[1], 64); got != want {
			t.Errorf("SRS %s = %v, label %v", name, got, want)
		}
	}
	if math.Float32bits(float32(ref.NoDataValue)) != lab.MissingConstantBits {
		t.Errorf("GDAL NoData %v is not MISSING_CONSTANT", ref.NoDataValue)
	}
	gdalMismatches(t, g, ref, true)
}

func TestGDALReferenceDetectsWrongConvention(t *testing.T) {
	ref := loadGDALReference(t)
	_, g := a02Georef(t)
	for _, shift := range []float64{0.5, -0.5, 1, -1} {
		wrong := g
		wrong.shift = shift
		if gdalMismatches(t, wrong, ref, false) == 0 {
			t.Errorf("shift %v was not detected by the GDAL reference", shift)
		}
	}
}

func TestA02LabelBoundsRegression(t *testing.T) {
	// The label's MINIMUM/MAXIMUM_LATITUDE and longitude bounds do not fall
	// on the raster edges (-0.5, W-0.5) under the verified convention; they
	// lie about one pixel off. Recorded in docs/MARSGEO.md; this pins the
	// observed values so a formula change cannot pass silently.
	lab, g := a02Georef(t)
	_, yMax := g.toPixel(Coordinate{Latitude: lab.MaximumLatitude, LongitudeEast: lab.CenterLongitude})
	_, yMin := g.toPixel(Coordinate{Latitude: lab.MinimumLatitude, LongitudeEast: lab.CenterLongitude})
	xW, _ := g.toPixel(Coordinate{Latitude: 15, LongitudeEast: lab.WesternmostLongitude})
	xE, _ := g.toPixel(Coordinate{Latitude: 15, LongitudeEast: lab.EasternmostLongitude})
	for name, c := range map[string][2]float64{
		"MAXIMUM_LATITUDE":      {yMax, 0.85},
		"MINIMUM_LATITUDE":      {yMin, 8252.51},
		"WESTERNMOST_LONGITUDE": {xW, 0.95},
		"EASTERNMOST_LONGITUDE": {xE, 3504.82},
	} {
		if math.Abs(c[0]-c[1]) > 0.01 {
			t.Errorf("%s maps to %.3f, recorded %.2f", name, c[0], c[1])
		}
	}
}

// --- Two radii, never mixed ---

func TestTwoRadiiAreSeparate(t *testing.T) {
	lab, g := a02Georef(t)
	centre := Coordinate{Latitude: 17.8, LongitudeEast: 232.49}
	corner := DefaultGrid.CornerLocal(50, 0) // 250 m east, 250 m north
	geo, err := LocalToGeographic(corner, centre)
	if err != nil {
		t.Fatal(err)
	}
	// Corner placement uses the client radius: recompute with R_client exactly.
	if want := centre.Latitude + 250/(radians*3_389_500); geo.Latitude != want {
		t.Fatalf("corner latitude %v, want %v (R = 3,389,500 m)", geo.Latitude, want)
	}
	// Using the product radius instead would move the corner by ~0.39 m.
	alt := centre.Latitude + 250/(radians*lab.RadiusMeters)
	if d := (geo.Latitude - alt) * radians * MarsMeanRadiusMeters; d < 0.3 || d > 0.5 {
		t.Fatalf("radius swap shift = %v m, expected ~0.39", d)
	}
	// Pixel lookup uses the product radius: y per degree of latitude.
	_, y0 := g.toPixel(Coordinate{Latitude: 17, LongitudeEast: 232.49})
	_, y1 := g.toPixel(Coordinate{Latitude: 18, LongitudeEast: 232.49})
	if want := lab.RadiusMeters * radians / lab.MapScale; math.Abs((y0-y1)-want) > 1e-6 {
		t.Fatalf("pixels per degree %v, want %v (R_p)", y0-y1, want)
	}
}

func TestRadiusConstantsHaveOneCallSite(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	counts := map[string]int{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		src := string(b)
		counts["clientRadius"] += strings.Count(src, "clientRadius")
		counts["RadiusMeters*"] += len(regexp.MustCompile(`l\.RadiusMeters\s*[*/]`).FindAllString(src, -1))
	}
	// clientRadius: declared once, used in GeographicToLocal (east, north) and
	// LocalToGeographic (lon, lat). Product radius: used only in newGeoref.
	if counts["clientRadius"] != 5 {
		t.Errorf("clientRadius appears %d times, want 5 (1 declaration + 4 uses in coords.go)", counts["clientRadius"])
	}
	if counts["RadiusMeters*"] != 2 {
		t.Errorf("product radius used %d times, want 2 (both in newGeoref)", counts["RadiusMeters*"])
	}
}
