package marsgeo

import (
	"errors"
	"fmt"
	"math"
)

// KernelSize is k = ceil(cellSize / MAP_SCALE): the number of sub-samples
// per axis in a corner's averaging footprint. It keeps the sub-sample
// spacing cellSize/k at or below one source pixel, which the coverage proof
// relies on. A02 (2.0195 m) gives 5; a 1 m product gives 10.
func KernelSize(cellSizeMeters, mapScale float64) int {
	return int(math.Ceil(cellSizeMeters / mapScale))
}

// kernelOffsets returns o_i = ((i + 0.5)/k - 0.5)·s for i = 0..k-1: the
// sub-sample offsets, in metres, from a corner along one local axis. For
// k = 5 and s = 10 m they are -4, -2, 0, 2, 4.
func kernelOffsets(k int, s float64) []float64 {
	o := make([]float64, k)
	for i := range o {
		o[i] = float64((float64(i)+0.5)/float64(k)-0.5) * s
	}
	return o
}

// errOutsideWindow is a programming error: the caller pre-read a window that
// does not hold the whole footprint. It is deliberately not ErrNoCoverage.
var errOutsideWindow = errors.New("marsgeo: stencil outside the pre-read window")

// DTMSource samples a product through a pre-read window. Each corner value is
// the equal-weight mean of a k x k grid of strict bilinear samples over the
// corner's own cellSize x cellSize footprint:
//
//	sub-sample (i, j) sits at local (E + o_i, N - o_j), j = 0 the north row;
//	each goes local -> geographic with MarsMeanRadiusMeters, then
//	geographic -> pixel with the product's own radius;
//	the sum runs north -> south, then west -> east, in float64.
//
// The result is a 10 m box mean of the DTM, not a point measurement. If any
// stencil pixel of any sub-sample is outside the raster or invalid, the
// corner fails with ErrNoCoverage; no value is ever filled in.
type DTMSource struct {
	prod     *Product
	win      *Window
	validity pixelValidity
	k        int
	cellSize float64
	offsets  []float64
}

// NewDTMSource checks k against the product's MAP_SCALE and returns a source
// that reads only from win.
func NewDTMSource(prod *Product, win *Window, k int, cellSize float64) (*DTMSource, error) {
	if want := KernelSize(cellSize, prod.Label.MapScale); k != want {
		return nil, fmt.Errorf("kernel %d does not match ceil(%v / %v) = %d", k, cellSize, prod.Label.MapScale, want)
	}
	return &DTMSource{prod: prod, win: win, validity: prod.validity(), k: k, cellSize: cellSize, offsets: kernelOffsets(k, cellSize)}, nil
}

// SampleCorner implements ElevationSource.
func (s *DTMSource) SampleCorner(center Coordinate, p LocalPoint, cellSize float64) (float64, error) {
	if cellSize != s.cellSize {
		return 0, fmt.Errorf("cell size %v does not match the source's %v", cellSize, s.cellSize)
	}
	var sum float64
	for j := 0; j < s.k; j++ {
		for i := 0; i < s.k; i++ {
			geo, err := LocalToGeographic(LocalPoint{East: p.East + s.offsets[i], North: p.North - s.offsets[j]}, center)
			if err != nil {
				return 0, err
			}
			x, y := s.prod.geo.toPixel(geo)
			v, err := s.bilinear(x, y)
			if err != nil {
				return 0, err
			}
			sum += v
		}
	}
	return sum / float64(s.k*s.k), nil
}

// bilinear interpolates at fractional pixel (x, y). All four stencil pixels
// must be inside the raster and valid, even when a weight is zero, so a
// sample exactly on the last column or line is rejected.
func (s *DTMSource) bilinear(x, y float64) (float64, error) {
	if math.IsNaN(x) || math.IsNaN(y) {
		return 0, fmt.Errorf("%w: pixel coordinate is NaN", ErrNoCoverage)
	}
	fx0, fy0 := math.Floor(x), math.Floor(y)
	lab := s.prod.Label
	if fx0 < 0 || fy0 < 0 || fx0+1 >= float64(lab.Samples) || fy0+1 >= float64(lab.Lines) {
		return 0, fmt.Errorf("%w: stencil at (%.3f, %.3f) leaves the %dx%d raster", ErrNoCoverage, x, y, lab.Samples, lab.Lines)
	}
	x0, y0 := int(fx0), int(fy0)
	if !s.win.Contains(x0, y0) || !s.win.Contains(x0+1, y0+1) {
		return 0, fmt.Errorf("%w at (%d, %d)", errOutsideWindow, x0, y0)
	}
	var p [4]float64
	for n, d := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		bits, v := s.win.At(x0+d[0], y0+d[1])
		if !s.validity.valid(bits) {
			return 0, fmt.Errorf("%w: invalid pixel (%d, %d), bits %#08x", ErrNoCoverage, x0+d[0], y0+d[1], bits)
		}
		p[n] = float64(v)
	}
	fx, fy := x-fx0, y-fy0
	// Explicit float64 conversions stop the compiler fusing these into FMA.
	top := float64((1-fx)*p[0]) + float64(fx*p[1])
	bottom := float64((1-fx)*p[2]) + float64(fx*p[3])
	return float64((1-fy)*top) + float64(fy*bottom), nil
}
