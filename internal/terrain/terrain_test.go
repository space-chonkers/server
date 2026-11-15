package terrain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"testing/fstest"
)

func mustLoad(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEmbeddedCatalog(t *testing.T) {
	c := mustLoad(t)
	products := map[string]bool{}
	for _, r := range c.Regions {
		if r.Datum != "mars-2000-areoid" || r.Source.ProductID == "" {
			t.Errorf("region %q: datum %q, product %q", r.ID, r.Datum, r.Source.ProductID)
		}
		products[r.Source.ProductID] = true
		t.Logf("%s: %d × %d samples, %d windows", r.ID, r.Columns, r.Rows, r.windows())
	}
	if len(c.Regions) < 4 || len(products) != len(c.Regions) {
		t.Fatalf("%d regions from %d products, want at least 4 distinct", len(c.Regions), len(products))
	}
	t.Logf("catalog %s: %d regions", c.Version, len(c.Regions))
}

// locate finds the region and window of a payload from its product and
// centre alone, independently of how Terrain chose them.
func locate(t *testing.T, c *Catalog, d Data) (r *region, top, left int) {
	t.Helper()
	for i := range c.Regions {
		r = &c.Regions[i]
		row := (r.NorthWestLatitude - d.Center.Latitude) / r.LatitudeStepDegrees
		col := (d.Center.LongitudeEast - r.NorthWestLongitudeEast) / r.LongitudeStepDegrees
		top, left = int(math.Round(row))-cells/2, int(math.Round(col))-cells/2
		onSample := math.Abs(row-math.Round(row)) < 1e-6 && math.Abs(col-math.Round(col)) < 1e-6
		if r.Source.ProductID == d.Source.ProductID && onSample &&
			top >= 0 && left >= 0 && top+samples <= r.Rows && left+samples <= r.Columns {
			return r, top, left
		}
	}
	t.Fatalf("seed %d: centre %+v is not a window centre of any %s region", d.Seed, d.Center, d.Source.ProductID)
	return nil, 0, 0
}

// TestTerrainMatchesHeightmap checks payloads from every region against the
// raw .f32 files, read independently, so a wrong row/column order, flip or
// centre offset fails.
func TestTerrainMatchesHeightmap(t *testing.T) {
	c := mustLoad(t)
	grids := map[string][]float32{}
	for _, r := range c.Regions {
		raw, err := os.ReadFile(r.File)
		if err != nil {
			t.Fatal(err)
		}
		grids[r.ID] = make([]float32, r.Columns*r.Rows)
		if err := binary.Read(bytes.NewReader(raw), binary.LittleEndian, grids[r.ID]); err != nil {
			t.Fatal(err)
		}
	}
	tested := map[string]int{}
	for _, seed := range append([]uint32{1 << 31, math.MaxUint32}, seq(200)...) {
		d := c.Terrain(seed)
		checkShape(t, d, seed)
		r, top, left := locate(t, c, d)
		if r.Datum != d.Elevation.Datum || r.Source != d.Source {
			t.Fatalf("seed %d: provenance differs from region %q", seed, r.ID)
		}
		for row := range samples {
			for col := range samples {
				if got, want := d.Elevation.SamplesMeters[row*samples+col], grids[r.ID][(top+row)*r.Columns+left+col]; got != want {
					t.Fatalf("seed %d: sample (%d, %d) = %v, want %v", seed, row, col, got, want)
				}
			}
		}
		tested[r.ID]++
	}
	if len(tested) != len(c.Regions) {
		t.Fatalf("202 seeds reached %d of %d regions", len(tested), len(c.Regions))
	}
}

func seq(n uint32) []uint32 {
	s := make([]uint32, n)
	for i := range s {
		s[i] = uint32(i)
	}
	return s
}

// checkShape applies the client parser's rules and the game's map size.
func checkShape(t *testing.T, d Data, seed uint32) {
	t.Helper()
	if d.Version != 1 || d.Seed != seed || d.Grid != (Grid{Columns: 50, Rows: 50, CellSizeMeters: 10}) {
		t.Fatalf("seed %d: version %d, seed %d, grid %+v", seed, d.Version, d.Seed, d.Grid)
	}
	e := d.Elevation
	if e.Sampling != "cell-corners" || e.Columns != 51 || e.Rows != 51 || len(e.SamplesMeters) != 51*51 || e.Datum != "mars-2000-areoid" {
		t.Fatalf("seed %d: elevation %s %dx%d, %d samples, datum %q", seed, e.Sampling, e.Columns, e.Rows, len(e.SamplesMeters), e.Datum)
	}
	for i, v := range e.SamplesMeters {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("seed %d: sample %d is %v", seed, i, v)
		}
	}
	if math.Abs(d.Center.Latitude) > 85 || d.Center.LongitudeEast < 0 || d.Center.LongitudeEast >= 360 {
		t.Fatalf("seed %d: centre %+v", seed, d.Center)
	}
	if s := d.Source; s.DatasetID == "" || s.Provider == "" || s.ResolutionMeters <= 0 {
		t.Fatalf("seed %d: source %+v", seed, s)
	}
}

func TestTerrainIsDeterministic(t *testing.T) {
	a, b := mustLoad(t), mustLoad(t)
	for _, seed := range []uint32{0, 7, 123456789} {
		first, _ := json.Marshal(a.Terrain(seed))
		second, _ := json.Marshal(b.Terrain(seed))
		if !bytes.Equal(first, second) {
			t.Fatalf("seed %d: two loads gave different payloads", seed)
		}
	}
}

// TestSeedsSpreadAcrossRegions checks that regions are chosen about equally
// often, whatever their size, and that windows cover each region.
func TestSeedsSpreadAcrossRegions(t *testing.T) {
	c := mustLoad(t)
	const n = 4000
	count, minTop, maxTop := map[string]int{}, map[string]int{}, map[string]int{}
	windows := map[Center]bool{}
	for _, seed := range seq(n) {
		d := c.Terrain(seed)
		r, top, _ := locate(t, c, d)
		if count[r.ID] == 0 {
			minTop[r.ID], maxTop[r.ID] = top, top
		}
		count[r.ID]++
		minTop[r.ID], maxTop[r.ID] = min(minTop[r.ID], top), max(maxTop[r.ID], top)
		windows[d.Center] = true
	}
	want := n / len(c.Regions)
	for _, r := range c.Regions {
		if math.Abs(float64(count[r.ID]-want)) > 0.15*float64(want) {
			t.Errorf("region %q chosen %d times, want about %d", r.ID, count[r.ID], want)
		}
		if span := maxTop[r.ID] - minTop[r.ID]; span < (r.Rows-samples)*9/10 {
			t.Errorf("region %q: window rows span %d of %d", r.ID, span, r.Rows-samples)
		}
	}
	if len(windows) < n*98/100 {
		t.Fatalf("%d seeds gave %d distinct windows", n, len(windows))
	}
}

func TestHandler(t *testing.T) {
	c := mustLoad(t)
	mux := http.NewServeMux()
	mux.Handle(Route, c)
	for path, want := range map[string]int{
		"/v1/mars-terrain/42":         http.StatusOK,
		"/v1/mars-terrain/4294967295": http.StatusOK,
		"/v1/mars-terrain/4294967296": http.StatusBadRequest,
		"/v1/mars-terrain/-1":         http.StatusBadRequest,
		"/v1/mars-terrain/042":        http.StatusBadRequest,
		"/v1/mars-terrain/x":          http.StatusBadRequest,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s: status %d, want %d", path, rec.Code, want)
		}
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mars-terrain/42", nil))
	var got Data
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("decode: %v, content type %q", err, rec.Header().Get("Content-Type"))
	}
	if !reflect.DeepEqual(got, c.Terrain(42)) {
		t.Fatal("HTTP body differs from Terrain(42)")
	}

	rec = httptest.NewRecorder()
	(*Catalog)(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("nil catalog without a seed: status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux = http.NewServeMux()
	mux.Handle(Route, (*Catalog)(nil))
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mars-terrain/1", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil catalog: status %d, want 503", rec.Code)
	}
}

// embeddedFiles is a complete, loadable copy of the embedded catalog files.
func embeddedFiles(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	names, _ := fs.Glob(embedded, "*")
	for _, name := range names {
		data, _ := fs.ReadFile(embedded, name)
		files[name] = &fstest.MapFile{Data: data}
	}
	if _, err := load(files); err != nil {
		t.Fatalf("intact copy: %v", err)
	}
	return files
}

// editCatalog returns catalog.json with edit applied to every region.
func editCatalog(t *testing.T, raw []byte, edit func(region, source map[string]any)) []byte {
	t.Helper()
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	for _, r := range c["regions"].([]any) {
		edit(r.(map[string]any), r.(map[string]any)["source"].(map[string]any))
	}
	out, _ := json.Marshal(c)
	return out
}

func TestNormalizeLongitude(t *testing.T) {
	for in, want := range map[float64]float64{
		0: 0, 232.5: 232.5, 359.75: 359.75, 360: 0, 365: 5, 725: 5, -5: 355, -360: 0, -1e-15: 0,
	} {
		if got := normalizeLongitude(in); got != want || got < 0 || got >= 360 {
			t.Errorf("normalizeLongitude(%v) = %v, want %v", in, got, want)
		}
	}
}

// TestRegionAcrossPrimeMeridian moves every region to straddle 0° E: all
// centres must still be in [0, 360), on both sides of the meridian.
func TestRegionAcrossPrimeMeridian(t *testing.T) {
	files := embeddedFiles(t)
	files["catalog.json"] = &fstest.MapFile{Data: editCatalog(t, files["catalog.json"].Data, func(r, _ map[string]any) {
		r["northWestLongitudeEast"] = -0.02
	})}
	c, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	west, east := 0, 0
	for _, seed := range seq(400) {
		lon := c.Terrain(seed).Center.LongitudeEast
		if lon < 0 || lon >= 360 {
			t.Fatalf("seed %d: longitude %v", seed, lon)
		}
		if lon > 359 {
			west++
		} else if lon < 1 {
			east++
		}
	}
	if west == 0 || east == 0 {
		t.Fatalf("centres west/east of 0° E: %d/%d, want both", west, east)
	}
}

func TestLoadRejectsBadData(t *testing.T) {
	files := embeddedFiles(t) // each case below breaks exactly one thing
	good := mustLoad(t).Regions[1]
	heights, catalogJSON := files[good.File].Data, files["catalog.json"].Data
	nan := bytes.Clone(heights)
	binary.LittleEndian.PutUint32(nan[4*1000:], math.Float32bits(float32(math.NaN())))
	sum := sha256.Sum256(nan)
	with := func(changes map[string][]byte) fstest.MapFS {
		m := maps.Clone(files)
		for name, data := range changes {
			if data == nil {
				delete(m, name)
			} else {
				m[name] = &fstest.MapFile{Data: data}
			}
		}
		return m
	}
	meta := func(edit func(region, source map[string]any)) fstest.MapFS {
		return with(map[string][]byte{"catalog.json": editCatalog(t, catalogJSON, edit)})
	}
	if _, err := load(meta(func(_, _ map[string]any) {})); err != nil {
		t.Fatalf("unedited round trip: %v", err)
	}
	cases := map[string]fstest.MapFS{
		"missing file": with(map[string][]byte{good.File: nil}),
		"truncated":    with(map[string][]byte{good.File: heights[:len(heights)-4]}),
		"altered":      with(map[string][]byte{good.File: nan}),
		"NaN height": with(map[string][]byte{good.File: nan,
			"catalog.json": bytes.ReplaceAll(catalogJSON, []byte(good.SHA256), []byte(hex.EncodeToString(sum[:])))}),
		"no regions":          with(map[string][]byte{"catalog.json": []byte(`{"version":"x","regions":[]}`)}),
		"datum missing":       meta(func(r, _ map[string]any) { delete(r, "datum") }),
		"datum unknown":       meta(func(r, _ map[string]any) { r["datum"] = "egm96" }),
		"provider blank":      meta(func(_, s map[string]any) { s["provider"] = "  " }),
		"dataset ID missing":  meta(func(_, s map[string]any) { delete(s, "datasetId") }),
		"resolution missing":  meta(func(_, s map[string]any) { delete(s, "resolutionMeters") }),
		"resolution negative": meta(func(_, s map[string]any) { s["resolutionMeters"] = -2.0 }),
		"catalog missing":     with(map[string][]byte{"catalog.json": nil}),
		"catalog malformed":   with(map[string][]byte{"catalog.json": []byte(`{"regions": [`)}),
		"spacing not 10 m":    meta(func(r, _ map[string]any) { r["spacingMeters"] = 20.0 }),
		"beyond 85° north":    meta(func(r, _ map[string]any) { r["northWestLatitude"] = 86.0 }),
		"west-running steps":  meta(func(r, _ map[string]any) { r["longitudeStepDegrees"] = -0.0001 }),
	}
	for name, fsys := range cases {
		if _, err := load(fsys); err == nil {
			t.Errorf("%s: load succeeded", name)
		}
	}
}
