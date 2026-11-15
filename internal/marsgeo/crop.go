package marsgeo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// CropProduct writes a re-labelled PDS3 product holding window win of the
// product whose label text is srcLabel. The new label keeps every source
// keyword and changes only what the crop changes: RECORD_BYTES,
// FILE_RECORDS, ^IMAGE, LINES, LINE_SAMPLES, the projection offsets (shifted
// by the window origin), the *_LAST_PIXEL keys, the lat/lon bounds (the
// crop's outer pixel edges, under the verified convention) and
// VALID_MINIMUM/MAXIMUM (recomputed over the crop's valid pixels only).
// comment is placed in a PDS comment at the top of the label.
func CropProduct(srcLabel []byte, src *Product, win *Window, comment string) ([]byte, error) {
	lab := src.Label
	pv := src.validity()
	vmin, vmax := math.Inf(1), math.Inf(-1)
	for _, b := range win.Bits {
		if pv.valid(b) {
			v := float64(math.Float32frombits(b))
			vmin, vmax = math.Min(vmin, v), math.Max(vmax, v)
		}
	}
	if math.IsInf(vmin, 0) {
		return nil, fmt.Errorf("%w: crop has no valid pixels", ErrNoCoverage)
	}
	text := srcLabel[:lab.LabelBytes]
	rb := win.W * 4

	// The crop's outer edges under the verified pixel convention.
	nw := src.geo.toGeographic(float64(win.X0)-0.5, float64(win.Y0)-0.5)
	se := src.geo.toGeographic(float64(win.X0+win.W)-0.5, float64(win.Y0+win.H)-0.5)

	f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	set := map[string]string{
		"RECORD_BYTES":             strconv.Itoa(rb),
		"LINES":                    strconv.Itoa(win.H),
		"LINE_SAMPLES":             strconv.Itoa(win.W),
		"LINE_LAST_PIXEL":          strconv.Itoa(win.H),
		"SAMPLE_LAST_PIXEL":        strconv.Itoa(win.W),
		"LINE_PROJECTION_OFFSET":   f(lab.LineOffset - float64(win.Y0)),
		"SAMPLE_PROJECTION_OFFSET": f(lab.SampleOffset - float64(win.X0)),
		// Exact float64 of the float32 extremes, so the bounds include them.
		"VALID_MINIMUM":         f(vmin),
		"VALID_MAXIMUM":         f(vmax),
		"MAXIMUM_LATITUDE":      f(nw.Latitude),
		"MINIMUM_LATITUDE":      f(se.Latitude),
		"WESTERNMOST_LONGITUDE": f(nw.LongitudeEast),
		"EASTERNMOST_LONGITUDE": f(se.LongitudeEast),
	}
	for key, value := range set {
		re := regexp.MustCompile(`(?m)^([ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=[ \t]*)[^ \t\r\n<]+`)
		if !re.Match(text) {
			if key == "LINE_LAST_PIXEL" || key == "SAMPLE_LAST_PIXEL" {
				continue // optional keys
			}
			return nil, fmt.Errorf("source label has no %s line to rewrite", key)
		}
		if n := len(re.FindAllIndex(text, -1)); n != 1 {
			return nil, fmt.Errorf("source label has %d %s lines", n, key)
		}
		text = re.ReplaceAll(text, []byte("${1}"+value))
	}
	header := []byte("/* " + comment + " */\r\n")
	body := append(header, text...)

	// Lay out: label padded to whole records, then the image. FILE_RECORDS and
	// ^IMAGE depend on the label length, so iterate until stable.
	var out []byte
	for records := 1; records < 1000; records++ {
		l := setValue(setValue(body, "^IMAGE", strconv.Itoa(records+1)), "FILE_RECORDS", strconv.Itoa(records+win.H))
		l = append(l, "\r\n"...)
		if len(l) <= records*rb {
			out = append(l, bytes.Repeat([]byte{' '}, records*rb-len(l))...)
			break
		}
	}
	if out == nil {
		return nil, fmt.Errorf("label does not fit")
	}
	raw := make([]byte, 4)
	for _, b := range win.Bits {
		binary.LittleEndian.PutUint32(raw, b)
		out = append(out, raw...)
	}
	// The crop must itself be a valid product.
	if _, err := OpenProduct(bytes.NewReader(out), int64(len(out))); err != nil {
		return nil, fmt.Errorf("crop does not re-open: %w", err)
	}
	return out, nil
}

func setValue(text []byte, key, value string) []byte {
	re := regexp.MustCompile(`(?m)^([ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=[ \t]*)[^ \t\r\n<]+`)
	return re.ReplaceAll(text, []byte("${1}"+value))
}
