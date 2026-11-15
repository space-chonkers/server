package marsgeo

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// maxLabelBytes bounds how much of a file is read to find the label END.
const maxLabelBytes = 1 << 20

// Product is an opened, verified HiRISE DTM: its label and random access to
// its raster. Samples are PC_REAL (little-endian float32), line by line,
// north to south, west to east.
type Product struct {
	Label *Label
	r     io.ReaderAt
	size  int64
	geo   georef
}

// OpenProduct parses and verifies the label at the start of r and checks
// that the file is long enough for the declared image. A short file is
// rejected; nothing is ever zero-filled.
func OpenProduct(r io.ReaderAt, size int64) (*Product, error) {
	head := make([]byte, min(size, maxLabelBytes))
	if _, err := r.ReadAt(head, 0); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read label: %w", err)
	}
	lab, err := ParseLabel(head)
	if err != nil {
		return nil, err
	}
	if need := lab.ImageOffset + lab.ImageBytes(); size < need {
		return nil, fmt.Errorf("%w: file has %d bytes, label declares %d", ErrInvalidProduct, size, need)
	}
	return &Product{Label: lab, r: r, size: size, geo: newGeoref(lab)}, nil
}

// WindowInside reports whether [x0, x0+w) x [y0, y0+h) is a non-empty window
// inside a samples x lines raster. It compares by subtraction, so no input,
// however large, can overflow (x0+w would wrap for w near MaxInt). Used by
// ReadWindow and by the prep tool's crop check.
func WindowInside(x0, y0, w, h, samples, lines int) bool {
	return w > 0 && h > 0 && x0 >= 0 && y0 >= 0 &&
		x0 < samples && y0 < lines && w <= samples-x0 && h <= lines-y0
}

// Window is a rectangle of raw samples, row-major from (X0, Y0).
type Window struct {
	X0, Y0, W, H int
	Bits         []uint32
}

// At returns the raw bits and value of pixel (x, y) in product coordinates.
func (w *Window) At(x, y int) (uint32, float32) {
	b := w.Bits[(y-w.Y0)*w.W+(x-w.X0)]
	return b, math.Float32frombits(b)
}

// Contains reports whether pixel (x, y) is inside the window.
func (w *Window) Contains(x, y int) bool {
	return x >= w.X0 && y >= w.Y0 && x < w.X0+w.W && y < w.Y0+w.H
}

// ReadWindow reads pixels [x0, x0+w) x [y0, y0+h). The window must lie inside
// the raster.
func (p *Product) ReadWindow(x0, y0, w, h int) (*Window, error) {
	lab := p.Label
	if !WindowInside(x0, y0, w, h, lab.Samples, lab.Lines) {
		return nil, fmt.Errorf("%w: window (%d,%d %dx%d) outside raster %dx%d", ErrNoCoverage, x0, y0, w, h, lab.Samples, lab.Lines)
	}
	win := &Window{X0: x0, Y0: y0, W: w, H: h, Bits: make([]uint32, w*h)}
	buf := make([]byte, w*4)
	for row := 0; row < h; row++ {
		off := lab.ImageOffset + (int64(y0+row)*int64(lab.Samples)+int64(x0))*4
		if _, err := p.r.ReadAt(buf, off); err != nil {
			return nil, fmt.Errorf("%w: read line %d: %v", ErrDataUnavailable, y0+row, err)
		}
		decodePCReal(win.Bits[row*w:(row+1)*w], buf)
	}
	return win, nil
}

// decodePCReal is the only decoding path: PC_REAL = little-endian IEEE 754
// binary32. The raw bits are kept so sentinels can be matched exactly.
func decodePCReal(dst []uint32, src []byte) {
	for i := range dst {
		dst[i] = binary.LittleEndian.Uint32(src[4*i:])
	}
}

// pixelValidity is the single definition of a usable elevation pixel, shared
// by resampling, the runtime re-check, the prep coverage scan and the crop's
// VALID range computation.
type pixelValidity struct {
	missingBits      uint32
	minimum, maximum float64
}

func (p *Product) validity() pixelValidity {
	return pixelValidity{missingBits: p.Label.MissingConstantBits, minimum: p.Label.ValidMinimum, maximum: p.Label.ValidMaximum}
}

// valid reports whether a raw sample is a usable elevation. The raw-bit
// checks run first and do not depend on the VALID range: 0xFF7FFFFB decodes
// to a finite float32, so NaN/Inf checks alone would accept it.
func (pv pixelValidity) valid(bits uint32) bool {
	if bits == pv.missingBits || isISISSpecialBits(bits) {
		return false
	}
	v := float64(math.Float32frombits(bits))
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	return v >= pv.minimum && v <= pv.maximum
}
