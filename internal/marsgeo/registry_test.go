package marsgeo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSynthRegistry writes p to dir and returns a one-entry registry for it.
func writeSynthRegistry(t *testing.T, dir string, p *synthProduct) (*Registry, []byte) {
	t.Helper()
	data := p.bytes()
	const file = "synthetic.IMG"
	if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
		t.Fatal(err)
	}
	prod, err := OpenProduct(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	lat, err := ScanLattice(prod, DefaultGrid, KernelSize(10, prod.Label.MapScale), 25)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	reg := &Registry{RegistryVersion: "test-1", Entries: []Entry{NewEntry(prod, file, hex.EncodeToString(sum[:]), int64(len(data)), "https://example.invalid/synthetic.IMG", lat)}}
	raw, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(raw); err != nil {
		t.Fatalf("registry round trip: %v", err)
	}
	return reg, raw
}

func TestSplitMix64Golden(t *testing.T) {
	// Reference SplitMix64 outputs (same algorithm as internal/game/random.go).
	r := splitMix64{state: 0}
	for i, want := range []uint64{0xe220a8397b1dcdaf, 0x6e789e6aa1b965f4, 0x06c45d188009454f} {
		if got := r.next(); got != want {
			t.Fatalf("seed 0 output %d = %#x want %#x", i, got, want)
		}
	}
	// Pinned selection sequence: changing it remaps every seed.
	counts := []int{1000}
	got := []int{}
	for _, seed := range []uint32{0, 1, 1234, 4294967295} {
		_, c := selectLocation(seed, counts)
		got = append(got, c)
	}
	want := []int{700, 519, 964, 860}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selection sequence %v, pinned %v", got, want)
		}
	}
}

func TestBuildFromSyntheticRegistry(t *testing.T) {
	dir := t.TempDir()
	reg, _ := writeSynthRegistry(t, dir, mapProductSized(232.49, 450))
	terr, err := reg.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer terr.Close()
	if n := reg.Entries[0].ValidCentres.Count(); n == 0 {
		t.Fatal("no valid centres in synthetic product")
	}
	a, err := terr.Build(1234)
	if err != nil {
		t.Fatal(err)
	}
	ab := mustValidPayload(t, &a)
	b, _ := terr.Build(1234)
	if bb, _ := json.Marshal(&b); !bytes.Equal(ab, bb) {
		t.Fatal("same seed gave different bytes")
	}
	if a.Elevation.Datum != Datum || a.Source.ProductID == nil || *a.Source.ProductID != "DTEED_TEST_A01" || a.Seed != 1234 {
		t.Fatalf("payload metadata wrong: %+v %+v", a.Elevation.Datum, a.Source)
	}
	if len(a.Elevation.SamplesMeters) != 51*51 {
		t.Fatal("wrong sample count")
	}
	for _, seed := range []uint32{0, 4294967295} {
		d, err := terr.Build(seed)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		mustValidPayload(t, &d)
	}
	// Different seeds reach different centres.
	centres := map[Coordinate]bool{}
	for seed := uint32(0); seed < 20; seed++ {
		centres[terr.Locate(seed).Center] = true
	}
	if len(centres) < 2 {
		t.Fatal("all seeds chose the same centre")
	}
}

func TestOpenFailuresAreDataUnavailable(t *testing.T) {
	mutate := map[string]func(t *testing.T, dir string, reg *Registry){
		"missing dir":  func(t *testing.T, dir string, reg *Registry) { os.RemoveAll(dir) },
		"missing file": func(t *testing.T, dir string, reg *Registry) { os.Remove(filepath.Join(dir, "synthetic.IMG")) },
		"wrong size":   func(t *testing.T, dir string, reg *Registry) { reg.Entries[0].ByteSize++ },
		"bad sha256": func(t *testing.T, dir string, reg *Registry) {
			reg.Entries[0].SHA256 = strings.Repeat("0", 64)
		},
		"label params differ": func(t *testing.T, dir string, reg *Registry) { reg.Entries[0].Projection.SampleOffset++ },
		"label fails verify": func(t *testing.T, dir string, reg *Registry) {
			p := mapProduct(232.49)
			p.edit("SAMPLE_TYPE", "  SAMPLE_TYPE = IEEE_REAL")
			data := p.bytes()
			os.WriteFile(filepath.Join(dir, "synthetic.IMG"), data, 0o644)
			sum := sha256.Sum256(data)
			reg.Entries[0].SHA256, reg.Entries[0].ByteSize = hex.EncodeToString(sum[:]), int64(len(data))
		},
	}
	for name, m := range mutate {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			reg, _ := writeSynthRegistry(t, dir, mapProduct(232.49))
			m(t, dir, reg)
			if _, err := reg.Open(dir); !errors.Is(err, ErrDataUnavailable) {
				t.Fatalf("err = %v, want ErrDataUnavailable", err)
			}
		})
	}
	reg := &Registry{RegistryVersion: "x"}
	if _, err := reg.Open(t.TempDir()); !errors.Is(err, ErrDataUnavailable) {
		t.Fatalf("empty registry: %v", err)
	}
	dir := t.TempDir()
	reg, _ = writeSynthRegistry(t, dir, mapProduct(232.49))
	if _, err := reg.Open(""); !errors.Is(err, ErrDataUnavailable) {
		t.Fatalf("unset data dir: %v", err)
	}
}

func TestLoadRegistryRejects(t *testing.T) {
	_, raw := writeSynthRegistry(t, t.TempDir(), mapProduct(232.49))
	cases := map[string][2]string{
		"unknown field":  {`"registryVersion"`, `"bogus":1,"registryVersion"`},
		"kernel":         {`"kernel":5`, `"kernel":4`},
		"grid":           {`"gridColumns":50`, `"gridColumns":40`},
		"no version":     {`"registryVersion":"test-1"`, `"registryVersion":""`},
		"path traversal": {`"file":"synthetic.IMG"`, `"file":"../synthetic.IMG"`},
		"empty lattice":  {`"runs":[`, `"runs":[],"x":[`},
	}
	for name, r := range cases {
		mutated := strings.Replace(string(raw), r[0], r[1], 1)
		if mutated == string(raw) {
			t.Fatalf("%s: replacement did not apply", name)
		}
		if _, err := LoadRegistry([]byte(mutated)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := DefaultRegistry(); err != nil {
		t.Fatalf("embedded registry: %v", err)
	}
}

func TestBuildRechecksPixelsAtRuntime(t *testing.T) {
	// The registry claims every centre is valid, but the file on disk (with a
	// matching checksum) has NoData inside the chosen footprint: Build must
	// fail with ErrNoCoverage, never sample around it.
	dir := t.TempDir()
	reg, _ := writeSynthRegistry(t, dir, mapProduct(232.49))
	open, err := reg.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	loc := open.Locate(7)
	open.Close()

	p := mapProduct(232.49)
	p.set(loc.PixelX, loc.PixelY, isisNull4Bits)
	data := p.bytes()
	os.WriteFile(filepath.Join(dir, "synthetic.IMG"), data, 0o644)
	sum := sha256.Sum256(data)
	reg.Entries[0].SHA256 = hex.EncodeToString(sum[:])
	terr, err := reg.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer terr.Close()
	if d, err := terr.Build(7); !errors.Is(err, ErrNoCoverage) || d.Elevation.SamplesMeters != nil {
		t.Fatalf("Build = %d samples, %v; want ErrNoCoverage", len(d.Elevation.SamplesMeters), err)
	}
}

func TestBuildSamplesMatchIndependentComputation(t *testing.T) {
	// Spot check: recompute the NW corner by hand from raw pixels.
	dir := t.TempDir()
	reg, _ := writeSynthRegistry(t, dir, mapProduct(232.49))
	terr, _ := reg.Open(dir)
	defer terr.Close()
	d, err := terr.Build(99)
	if err != nil {
		t.Fatal(err)
	}
	prod := terr.products[0]
	var sum float64
	for j := 0; j < 5; j++ {
		for i := 0; i < 5; i++ {
			e := -250 + float64(-4+2*i)
			n := 250 - float64(-4+2*j)
			lat := d.Center.Latitude + n/(radians*3_389_500)
			lon := d.Center.LongitudeEast + e/(radians*3_389_500*math.Cos(d.Center.Latitude*radians))
			x, y := prod.geo.toPixel(Coordinate{Latitude: lat, LongitudeEast: lon})
			// The ramp is value = 1000·row + col, and bilinear is exact on it.
			sum += 1000*y + x
		}
	}
	if want := sum / 25; math.Abs(d.Elevation.SamplesMeters[0]-want) > 1e-6 {
		t.Fatalf("NW corner %v, independent %v", d.Elevation.SamplesMeters[0], want)
	}
}

func TestRegistryRejectsUnsafeNames(t *testing.T) {
	_, raw := writeSynthRegistry(t, t.TempDir(), mapProduct(232.49))
	for name, r := range map[string][2]string{
		"file .":             {`"file":"synthetic.IMG"`, `"file":"."`},
		"file ..":            {`"file":"synthetic.IMG"`, `"file":".."`},
		"file with dir":      {`"file":"synthetic.IMG"`, `"file":"a/synthetic.IMG"`},
		"productId traverse": {`"productId":"DTEED_TEST_A01"`, `"productId":"DTEE/../../x"`},
		"productId lower":    {`"productId":"DTEED_TEST_A01"`, `"productId":"dteed_test_a01"`},
	} {
		mutated := strings.Replace(string(raw), r[0], r[1], 1)
		if mutated == string(raw) {
			t.Fatalf("%s: replacement did not apply", name)
		}
		if _, err := LoadRegistry([]byte(mutated)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTerrainCloseReportsErrors(t *testing.T) {
	dir := t.TempDir()
	reg, _ := writeSynthRegistry(t, dir, mapProduct(232.49))
	terr, err := reg.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := terr.files[0].Close(); err != nil { // closed behind Terrain's back
		t.Fatal(err)
	}
	if err := terr.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Close = %v, want an error wrapping os.ErrClosed", err)
	}
	if err := terr.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}

	ok, err := reg.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ok.Close(); err != nil {
		t.Fatalf("clean Close = %v", err)
	}
}
