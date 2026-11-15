package marsgeo

import (
	"errors"
	"fmt"
	"math"
)

// Rect is an inclusive pixel rectangle [X0, X1] x [Y0, Y1].
type Rect struct{ X0, Y0, X1, Y1 int }

func (r Rect) Width() int  { return r.X1 - r.X0 + 1 }
func (r Rect) Height() int { return r.Y1 - r.Y0 + 1 }

// FootprintRect returns exactly the set of pixels the bilinear stencils of a
// whole map touch, as a rectangle.
//
// Why a rectangle is exact: the client mapping is separable (longitude depends
// only on east, latitude only on north) and so is the product projection (x
// only on longitude, y only on latitude), so pixel x is monotonic in east alone
// and y in north alone. Consecutive sub-samples along an axis are cellSize/k
// metres apart, which is under 2 pixels (checked here), so consecutive
// floor(x) differ by at most 2 and the 2-wide stencils leave no gap. The
// touched columns are therefore every column from floor(x_min) to
// floor(x_max)+1, and likewise for rows.
//
// The rectangle may extend outside the raster; callers treat that as no
// coverage.
func FootprintRect(g georef, center Coordinate, grid Grid, k int) (Rect, error) {
	if err := checkMapCenter(center); err != nil {
		return Rect{}, err
	}
	o := kernelOffsets(k, grid.CellSizeMeters)
	west := grid.CornerLocal(0, 0).East + o[0]
	east := grid.CornerLocal(grid.Columns, 0).East + o[k-1]
	north := grid.CornerLocal(0, 0).North - o[0]
	south := grid.CornerLocal(0, grid.Rows).North - o[k-1]
	px := func(e, n float64) (float64, float64, error) {
		geo, err := LocalToGeographic(LocalPoint{East: e, North: n}, center)
		if err != nil {
			return 0, 0, err
		}
		x, y := g.toPixel(geo)
		return x, y, nil
	}
	xMin, yMin, err := px(west, north)
	if err != nil {
		return Rect{}, err
	}
	xMax, yMax, err := px(east, south)
	if err != nil {
		return Rect{}, err
	}
	// Sub-sample spacing in pixels, measured on this map.
	step := grid.CellSizeMeters / float64(k)
	xs, ys, err := px(west+step, north-step)
	if err != nil {
		return Rect{}, err
	}
	if dx, dy := xs-xMin, ys-yMin; dx <= 0 || dy <= 0 || dx >= 2 || dy >= 2 {
		return Rect{}, fmt.Errorf("sub-sample spacing (%.3f, %.3f) px outside (0, 2): kernel %d too small", dx, dy, k)
	}
	return Rect{
		X0: int(math.Floor(xMin)), X1: int(math.Floor(xMax)) + 1,
		Y0: int(math.Floor(yMin)), Y1: int(math.Floor(yMax)) + 1,
	}, nil
}

// inside reports whether r lies within a w x h raster.
func (r Rect) inside(w, h int) bool {
	return r.X0 >= 0 && r.Y0 >= 0 && r.X1 < w && r.Y1 < h
}

// invalidInWindow counts invalid pixels of rect r inside a pre-read window
// (the runtime re-check). r must lie within the window.
func invalidInWindow(win *Window, r Rect, pv pixelValidity) int {
	n := 0
	for y := r.Y0; y <= r.Y1; y++ {
		for x := r.X0; x <= r.X1; x++ {
			if bits, _ := win.At(x, y); !pv.valid(bits) {
				n++
			}
		}
	}
	return n
}

// invalidPrefix is a 2-D prefix sum of invalid pixels: sum[(y)*(w+1)+x] is
// the count over [0, x) x [0, y). Used by the prep scan.
type invalidPrefix struct {
	w, h int
	sum  []uint32
}

// PrefixScanBudgetBytes is the most memory the coverage scan's prefix-sum
// table may use: (LINE_SAMPLES+1)·(LINES+1)·4 bytes. 512 MiB holds about 134
// million cells; the real A02 product (3,504 x 8,252) needs about 116 MB.
// Larger rasters are refused with ErrScanBudgetExceeded before anything is
// allocated, so a malformed or oversized label cannot drive the prep tool
// into a multi-terabyte allocation.
const PrefixScanBudgetBytes int64 = 512 << 20

// ErrScanBudgetExceeded reports a raster too large for the coverage scan's
// memory budget.
var ErrScanBudgetExceeded = errors.New("marsgeo: raster exceeds the coverage-scan memory budget")

// prefixTableBytes returns the prefix-sum table size for a w x h raster, or
// false if it exceeds budget. The arithmetic is checked before multiplying, so
// it cannot overflow for any int dimensions.
func prefixTableBytes(w, h int, budget int64) (int64, bool) {
	maxCells := budget / 4
	// Compare before adding 1: int64(w)+1 would wrap for w = MaxInt64.
	if w < 0 || h < 0 || int64(w) >= maxCells || int64(h) >= maxCells {
		return 0, false
	}
	cols, rows := int64(w)+1, int64(h)+1 // both now <= maxCells <= 2^61
	if rows > maxCells/cols {
		return 0, false
	}
	return cols * rows * 4, true
}

// newInvalidPrefix scans the product line by line within PrefixScanBudgetBytes.
func newInvalidPrefix(p *Product) (*invalidPrefix, error) {
	return newInvalidPrefixBudget(p, PrefixScanBudgetBytes)
}

func newInvalidPrefixBudget(p *Product, budget int64) (*invalidPrefix, error) {
	w, h := p.Label.Samples, p.Label.Lines
	size, ok := prefixTableBytes(w, h, budget)
	if !ok {
		return nil, fmt.Errorf("%w: %s is %dx%d px; its prefix-sum table would exceed %d bytes", ErrScanBudgetExceeded, p.Label.ProductID, w, h, budget)
	}
	ip := &invalidPrefix{w: w, h: h, sum: make([]uint32, size/4)}
	pv := p.validity()
	for y := 0; y < h; y++ {
		line, err := p.ReadWindow(0, y, w, 1)
		if err != nil {
			return nil, err
		}
		var rowSum uint32
		for x := 0; x < w; x++ {
			if !pv.valid(line.Bits[x]) {
				rowSum++
			}
			ip.sum[(y+1)*(w+1)+x+1] = ip.sum[y*(w+1)+x+1] + rowSum
		}
	}
	return ip, nil
}

// count returns the number of invalid pixels in r, which must be inside the raster.
func (ip *invalidPrefix) count(r Rect) uint32 {
	w1 := ip.w + 1
	return ip.sum[(r.Y1+1)*w1+r.X1+1] - ip.sum[r.Y0*w1+r.X1+1] - ip.sum[(r.Y1+1)*w1+r.X0] + ip.sum[r.Y0*w1+r.X0]
}

// Lattice is the set of valid map centres of one product: pixel centres on
// a regular grid (OriginX + c·Step, OriginY + r·Step), stored as runs
// [latticeRow, firstCol, lastCol] in ascending order.
type Lattice struct {
	OriginX int      `json:"originX"`
	OriginY int      `json:"originY"`
	Step    int      `json:"step"`
	Runs    [][3]int `json:"runs"`
}

// Count is the number of valid centres.
func (l *Lattice) Count() int {
	n := 0
	for _, r := range l.Runs {
		n += r[2] - r[1] + 1
	}
	return n
}

// Pixel returns the pixel of the n-th valid centre, in run order.
func (l *Lattice) Pixel(n int) (x, y int, ok bool) {
	if n < 0 {
		return 0, 0, false
	}
	for _, r := range l.Runs {
		if c := r[2] - r[1] + 1; n < c {
			return l.OriginX + (r[1]+n)*l.Step, l.OriginY + r[0]*l.Step, true
		} else {
			n -= c
		}
	}
	return 0, 0, false
}

// validate checks the runs are well formed and strictly ordered.
func (l *Lattice) validate() error {
	if l.Step <= 0 || l.OriginX < 0 || l.OriginY < 0 {
		return fmt.Errorf("lattice origin/step invalid")
	}
	prevRow, prevLast := -1, -1
	for _, r := range l.Runs {
		if r[0] < 0 || r[1] < 0 || r[2] < r[1] || r[0] < prevRow || (r[0] == prevRow && r[1] <= prevLast+1) {
			return fmt.Errorf("lattice run %v out of order or malformed", r)
		}
		prevRow, prevLast = r[0], r[2]
	}
	return nil
}

// ScanLattice finds every lattice centre whose whole map footprint is inside
// the raster and free of invalid pixels. step is in pixels.
func ScanLattice(p *Product, grid Grid, k, step int) (*Lattice, error) {
	ip, err := newInvalidPrefix(p)
	if err != nil {
		return nil, err
	}
	return scanLattice(p, ip, grid, k, step)
}

func scanLattice(p *Product, ip *invalidPrefix, grid Grid, k, step int) (*Lattice, error) {
	lat := &Lattice{Step: step}
	w, h := p.Label.Samples, p.Label.Lines
	for row := 0; row*step < h; row++ {
		first := -1
		flush := func(last int) {
			if first >= 0 {
				lat.Runs = append(lat.Runs, [3]int{row, first, last})
				first = -1
			}
		}
		for col := 0; col*step < w; col++ {
			center := p.geo.toGeographic(float64(col*step), float64(row*step))
			ok := math.Abs(center.Latitude) <= MaxMapLatitudeDegrees
			if ok {
				r, err := FootprintRect(p.geo, center, grid, k)
				if err != nil {
					return nil, err
				}
				ok = r.inside(w, h) && ip.count(r) == 0
			}
			switch {
			case ok && first < 0:
				first = col
			case !ok:
				flush(col - 1)
			}
		}
		flush((w - 1) / step)
	}
	return lat, nil
}
