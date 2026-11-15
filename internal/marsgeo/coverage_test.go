package marsgeo

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"testing"
)

// mapProduct is a 300 x 300 px, 2 m synthetic product (k = 5) large enough for
// a 500 m map centred on pixel (150, 150). centerLon sets where the product
// sits; 0 makes the map straddle 0°/360°.
func mapProduct(centerLon float64) *synthProduct { return mapProductSized(centerLon, 300) }

// mapProductSized is an n x n version with pixel (n/2, n/2) at 17°N, centerLon.
func mapProductSized(centerLon float64, n int) *synthProduct {
	p := newSynthProduct(n, n)
	p.CenterLat, p.CenterLon = 17, centerLon
	p.SampleOffset = float64(n / 2)
	p.LineOffset = 17*math.Pi/180*p.RadiusKm*1000/p.MapScale + float64(n/2)
	return p
}

func mapCenter(t *testing.T, p *Product) Coordinate {
	t.Helper()
	return p.geo.toGeographic(150, 150)
}

// touchedPixels brute-forces every stencil pixel of every sub-sample of a map.
func touchedPixels(t *testing.T, p *Product, center Coordinate, grid Grid, k int) map[[2]int]bool {
	t.Helper()
	o := kernelOffsets(k, grid.CellSizeMeters)
	set := map[[2]int]bool{}
	for row := 0; row <= grid.Rows; row++ {
		for col := 0; col <= grid.Columns; col++ {
			c := grid.CornerLocal(col, row)
			for j := 0; j < k; j++ {
				for i := 0; i < k; i++ {
					geo, err := LocalToGeographic(LocalPoint{East: c.East + o[i], North: c.North - o[j]}, center)
					if err != nil {
						t.Fatal(err)
					}
					x, y := p.geo.toPixel(geo)
					x0, y0 := int(math.Floor(x)), int(math.Floor(y))
					for _, d := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
						set[[2]int{x0 + d[0], y0 + d[1]}] = true
					}
				}
			}
		}
	}
	return set
}

func TestFootprintRectIsExact(t *testing.T) {
	for _, lon := range []float64{232.49, 0} {
		t.Run(fmt.Sprintf("lon %v", lon), func(t *testing.T) {
			p := mapProduct(lon).open(t)
			for _, d := range [][2]float64{{0, 0}, {0.3, -0.7}, {-12.5, 7.25}} {
				center := p.geo.toGeographic(150+d[0], 150+d[1])
				k := KernelSize(10, p.Label.MapScale)
				r, err := FootprintRect(p.geo, center, DefaultGrid, k)
				if err != nil {
					t.Fatal(err)
				}
				set := touchedPixels(t, p, center, DefaultGrid, k)
				if len(set) != r.Width()*r.Height() {
					t.Fatalf("centre %v: brute force touches %d pixels, rect %+v has %d", d, len(set), r, r.Width()*r.Height())
				}
				for px := range set {
					if px[0] < r.X0 || px[0] > r.X1 || px[1] < r.Y0 || px[1] > r.Y1 {
						t.Fatalf("pixel %v outside rect %+v", px, r)
					}
				}
			}
			if lon == 0 {
				c := mapCenter(t, p)
				w, _ := LocalToGeographic(DefaultGrid.CornerLocal(0, 0), c)
				e, _ := LocalToGeographic(DefaultGrid.CornerLocal(50, 0), c)
				if !(w.LongitudeEast > 359 && e.LongitudeEast < 1) {
					t.Fatalf("map does not straddle 0/360: %v .. %v", w.LongitudeEast, e.LongitudeEast)
				}
			}
		})
	}
}

func TestPrefixSumMatchesDirectScan(t *testing.T) {
	p := mapProduct(232.49)
	for i := 0; i < len(p.Bits); i += 97 {
		p.Bits[i] = isisNull4Bits
	}
	prod := p.open(t)
	ip, err := newInvalidPrefix(prod)
	if err != nil {
		t.Fatal(err)
	}
	win, _ := prod.ReadWindow(0, 0, 300, 300)
	for _, r := range []Rect{{0, 0, 299, 299}, {10, 20, 10, 20}, {5, 7, 123, 250}, {0, 0, 0, 299}} {
		if a, b := ip.count(r), invalidInWindow(win, r, prod.validity()); int(a) != b {
			t.Errorf("rect %+v: prefix %d, scan %d", r, a, b)
		}
	}
}

// sentinelKinds is every invalid-pixel kind the coverage checks must catch.
var sentinelKinds = map[string]uint32{
	"MISSING_CONSTANT": 0xFF7FFFFB,
	"ISIS LRS":         0xFF7FFFFC,
	"ISIS HRS":         0xFF7FFFFF,
	"ISIS low bound":   0xFF7FFFFA,
	"quiet NaN":        0x7FC00000,
	"negative NaN":     0xFFC00001,
	"+Inf":             0x7F800000,
	"-Inf":             0xFF800000,
	"below VALID_MIN":  math.Float32bits(-9001),
	"above VALID_MAX":  math.Float32bits(2e6),
}

func TestSentinelsNeverPassCoverage(t *testing.T) {
	base := mapProduct(232.49).open(t)
	center := mapCenter(t, base)
	k := KernelSize(10, base.Label.MapScale)
	r, err := FootprintRect(base.geo, center, DefaultGrid, k)
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string][2]int{
		"rect centre": {(r.X0 + r.X1) / 2, (r.Y0 + r.Y1) / 2},
		"NW corner":   {r.X0, r.Y0},
		"NE corner":   {r.X1, r.Y0},
		"SW corner":   {r.X0, r.Y1},
		"SE corner":   {r.X1, r.Y1},
	}
	step := 150 // lattice of (0,0), (150,0), (0,150), (150,150), ...: (150,150) is our centre
	for kind, bits := range sentinelKinds {
		for where, px := range positions {
			t.Run(kind+"/"+where, func(t *testing.T) {
				p := mapProduct(232.49)
				p.set(px[0], px[1], bits)
				prod := p.open(t)
				win, err := prod.ReadWindow(r.X0, r.Y0, r.Width(), r.Height())
				if err != nil {
					t.Fatal(err)
				}
				if n := invalidInWindow(win, r, prod.validity()); n != 1 {
					t.Errorf("runtime re-check found %d invalid pixels, want 1", n)
				}
				ip, _ := newInvalidPrefix(prod)
				if n := ip.count(r); n != 1 {
					t.Errorf("prefix sum found %d, want 1", n)
				}
				lat, err := scanLattice(prod, ip, DefaultGrid, k, step)
				if err != nil {
					t.Fatal(err)
				}
				for n := 0; n < lat.Count(); n++ {
					if x, y, _ := lat.Pixel(n); x == 150 && y == 150 {
						t.Errorf("lattice kept centre (150,150)")
					}
				}
				src, _ := NewDTMSource(prod, win, k, 10)
				samples, err := Generate(center, DefaultGrid, src)
				if !errors.Is(err, ErrNoCoverage) || samples != nil {
					t.Errorf("Generate: %d samples, err %v", len(samples), err)
				}
			})
		}
	}
	// The same sentinel one column outside the rectangle changes nothing.
	p := mapProduct(232.49)
	p.set(r.X1+1, (r.Y0+r.Y1)/2, isisNull4Bits)
	p.set(r.X0-1, r.Y0, isisNull4Bits)
	prod := p.open(t)
	ip, _ := newInvalidPrefix(prod)
	if n := ip.count(r); n != 0 {
		t.Fatalf("pixels outside the rect counted: %d", n)
	}
	win, _ := prod.ReadWindow(r.X0, r.Y0, r.Width(), r.Height())
	src, _ := NewDTMSource(prod, win, k, 10)
	if _, err := Generate(center, DefaultGrid, src); err != nil {
		t.Fatalf("sentinel outside the footprint broke generation: %v", err)
	}
	lat, _ := scanLattice(prod, ip, DefaultGrid, k, step)
	found := false
	for n := 0; n < lat.Count(); n++ {
		if x, y, _ := lat.Pixel(n); x == 150 && y == 150 {
			found = true
		}
	}
	if !found {
		t.Fatal("lattice dropped a fully valid centre")
	}
}

func TestSuccessfulSamplesAreAlwaysValid(t *testing.T) {
	// Property: over a raster seeded with sentinels, every map that does
	// generate has only finite, in-range, non-special samples.
	p := mapProduct(232.49)
	// Sentinels go in the outer 15 columns on each side, at random rows, so
	// centred footprints are clean and shifted ones are not.
	rng := uint64(42)
	kinds := []uint32{0xFF7FFFFB, 0x7FC00000, 0xFF800000, math.Float32bits(-9001)}
	for n := 0; n < 40; n++ {
		rng = rng*6364136223846793005 + 1442695040888963407
		col := int(rng>>40) % 15
		if n%2 == 1 {
			col = 299 - col
		}
		p.set(col, int(rng>>20)%300, kinds[n%len(kinds)])
	}
	prod := p.open(t)
	k := KernelSize(10, prod.Label.MapScale)
	ok, failed := 0, 0
	for dy := -40; dy <= 40; dy += 8 {
		for dx := -40; dx <= 40; dx += 8 {
			center := prod.geo.toGeographic(150+float64(dx), 150+float64(dy))
			r, err := FootprintRect(prod.geo, center, DefaultGrid, k)
			if err != nil {
				t.Fatal(err)
			}
			win, err := prod.ReadWindow(r.X0, r.Y0, r.Width(), r.Height())
			if err != nil {
				failed++
				continue
			}
			src, _ := NewDTMSource(prod, win, k, 10)
			samples, err := Generate(center, DefaultGrid, src)
			if err != nil {
				if !errors.Is(err, ErrNoCoverage) {
					t.Fatal(err)
				}
				if invalidInWindow(win, r, prod.validity()) == 0 {
					t.Fatalf("clean footprint failed: %v", err)
				}
				failed++
				continue
			}
			if invalidInWindow(win, r, prod.validity()) != 0 {
				t.Fatal("footprint with invalid pixels generated a map")
			}
			ok++
			for _, v := range samples {
				if math.IsNaN(v) || math.IsInf(v, 0) || v < prod.Label.ValidMinimum || v > prod.Label.ValidMaximum || isISISSpecialBits(math.Float32bits(float32(v))) {
					t.Fatalf("sample %v escaped validation", v)
				}
			}
		}
	}
	if ok == 0 || failed == 0 {
		t.Fatalf("property test not exercising both outcomes: ok %d failed %d", ok, failed)
	}
}

func TestLatticeRuns(t *testing.T) {
	l := Lattice{OriginX: 0, OriginY: 0, Step: 25, Runs: [][3]int{{1, 2, 4}, {3, 0, 0}}}
	if err := l.validate(); err != nil {
		t.Fatal(err)
	}
	if l.Count() != 4 {
		t.Fatalf("count %d", l.Count())
	}
	want := [][2]int{{50, 25}, {75, 25}, {100, 25}, {0, 75}}
	for n, w := range want {
		if x, y, ok := l.Pixel(n); !ok || x != w[0] || y != w[1] {
			t.Errorf("Pixel(%d) = %d,%d,%v want %v", n, x, y, ok, w)
		}
	}
	if _, _, ok := l.Pixel(4); ok {
		t.Error("Pixel past the end")
	}
	for _, bad := range [][][3]int{{{1, 4, 2}}, {{2, 0, 1}, {1, 0, 1}}, {{1, 0, 3}, {1, 4, 5}}} {
		if (&Lattice{Step: 1, Runs: bad}).validate() == nil {
			t.Errorf("runs %v accepted", bad)
		}
	}
}

func TestPrefixTableBudget(t *testing.T) {
	const b = PrefixScanBudgetBytes
	a02, ok := prefixTableBytes(3504, 8252, b)
	if !ok || a02 != 3505*8253*4 {
		t.Fatalf("A02 table = %d, %v; want %d within the budget", a02, ok, 3505*8253*4)
	}
	cells := b / 4
	for _, c := range []struct {
		w, h int
		ok   bool
	}{
		{0, 0, true},
		{int(cells) - 1, 0, true},         // exactly the budget
		{int(cells), 0, false},            // one cell over
		{1 << 20, 1 << 20, false},         // largest label-legal raster: ~4 TiB
		{math.MaxInt, math.MaxInt, false}, // would overflow a naive (w+1)*(h+1)
		{math.MaxInt, 0, false},
		{-1, 5, false},
	} {
		if _, got := prefixTableBytes(c.w, c.h, b); got != c.ok {
			t.Errorf("prefixTableBytes(%d, %d) ok = %v, want %v", c.w, c.h, got, c.ok)
		}
	}
}

func TestPrefixScanRefusesOverBudget(t *testing.T) {
	prod := newSynthProduct(40, 30).open(t)
	if _, err := newInvalidPrefixBudget(prod, 41*31*4-1); !errors.Is(err, ErrScanBudgetExceeded) {
		t.Fatalf("err = %v, want ErrScanBudgetExceeded", err)
	}
	if _, err := newInvalidPrefixBudget(prod, 41*31*4); err != nil {
		t.Fatalf("exact budget rejected: %v", err)
	}
}

// hugeReader serves a label and claims a size big enough for any declared
// image; image reads return zeros. Nothing must ever read that far.
type hugeReader struct{ label []byte }

func (h hugeReader) ReadAt(p []byte, off int64) (int, error) {
	for i := range p {
		p[i] = 0
	}
	if off < int64(len(h.label)) {
		copy(p, h.label[off:])
	}
	return len(p), nil
}

func TestScanLatticeRefusesOversizedLabelWithoutAllocating(t *testing.T) {
	p := newSynthProduct(4, 4)
	p.edit("LINES", "  LINES = 1048576")
	p.edit("LINE_SAMPLES", "  LINE_SAMPLES = 1048576")
	p.edit("RECORD_BYTES", "RECORD_BYTES = 4194304")
	p.imagePointer = func(rb, records int) (string, int) { return "2", 4194304 } // record 2 of the forged RECORD_BYTES
	label := p.bytes()
	prod, err := OpenProduct(hugeReader{label}, 1<<62)
	if err != nil {
		t.Fatalf("forged label should parse (it is layout-consistent): %v", err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err = ScanLattice(prod, DefaultGrid, 5, 25)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrScanBudgetExceeded) {
		t.Fatalf("err = %v, want ErrScanBudgetExceeded", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("refusal allocated %d bytes", grew)
	}
}
