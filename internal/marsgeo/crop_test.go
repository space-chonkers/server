package marsgeo

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestCropProduct(t *testing.T) {
	p := newSynthProduct(60, 50)
	// Valid extremes inside the window, and sentinels that must not count.
	p.set(12, 9, math.Float32bits(-8000.5))
	p.set(30, 20, math.Float32bits(999999.25))
	p.set(13, 9, isisNull4Bits)
	p.set(14, 9, math.Float32bits(-9500)) // below the source VALID_MINIMUM: invalid, not a new minimum
	src := p.bytes()
	prod, err := OpenProduct(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	win, err := prod.ReadWindow(10, 8, 25, 20)
	if err != nil {
		t.Fatal(err)
	}
	out, err := CropProduct(src, prod, win, "test crop")
	if err != nil {
		t.Fatal(err)
	}
	crop, err := OpenProduct(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	l := crop.Label
	if l.Samples != 25 || l.Lines != 20 || l.RecordBytes != 100 || l.ProductID != prod.Label.ProductID {
		t.Fatalf("crop label: %+v", l)
	}
	if l.ValidMinimum != -8000.5 || l.ValidMaximum != 999999.25 {
		t.Fatalf("VALID range %v..%v, want -8000.5..999999.25", l.ValidMinimum, l.ValidMaximum)
	}
	if !strings.Contains(string(out[:l.ImageOffset]), "/* test crop */") {
		t.Fatal("provenance comment missing")
	}
	// Every crop pixel has the same bits and the same geographic centre as its source pixel.
	cw, _ := crop.ReadWindow(0, 0, 25, 20)
	for y := 0; y < 20; y++ {
		for x := 0; x < 25; x++ {
			a, _ := cw.At(x, y)
			b, _ := win.At(x+10, y+8)
			if a != b {
				t.Fatalf("pixel (%d,%d) bits differ", x, y)
			}
			gc, gs := crop.geo.toGeographic(float64(x), float64(y)), prod.geo.toGeographic(float64(x+10), float64(y+8))
			if math.Abs(gc.Latitude-gs.Latitude) > 1e-12 || math.Abs(longitudeDelta(gc.LongitudeEast, gs.LongitudeEast)) > 1e-12 {
				t.Fatalf("pixel (%d,%d) moved: %+v vs %+v", x, y, gc, gs)
			}
		}
	}
	// The recomputed VALID range keeps the extremes valid and the sentinels invalid.
	pv := crop.validity()
	if b, _ := cw.At(2, 1); !pv.valid(b) {
		t.Error("crop minimum pixel is invalid under the crop's own label")
	}
	if b, _ := cw.At(3, 1); pv.valid(b) {
		t.Error("sentinel valid in crop")
	}
}
