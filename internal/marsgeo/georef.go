package marsgeo

import "math"

// pixelIndexShift converts the PDS projection offsets into this package's
// 0-based pixel coordinates, where integer (i, j) is the centre of pixel
// (i, j). It is the one place the pixel convention lives; docs/MARSGEO.md
// records the evidence (published convention + GDAL reference) behind its
// value.
const pixelIndexShift = 0.0

// georef is a product's equirectangular geographic <-> fractional pixel
// mapping. It uses the product's own projection sphere (label A_AXIS_RADIUS),
// never MarsMeanRadiusMeters.
//
//	x = SAMPLE_PROJECTION_OFFSET + rad(Δλ)·R_p·cos(rad φ1)/MAP_SCALE - shift
//	y = LINE_PROJECTION_OFFSET   - rad(φ)·R_p/MAP_SCALE           - shift
//
// with Δλ the signed shortest difference from CENTER_LONGITUDE and φ1 the
// standard parallel CENTER_LATITUDE. x grows east, y grows south; pixel
// (0, 0) is the north-west pixel and covers [-0.5, 0.5) on both axes.
type georef struct {
	sampleOffset, lineOffset float64
	centerLon                float64
	pxPerRadLon              float64 // R_p·cos(φ1)/MAP_SCALE
	pxPerRadLat              float64 // R_p/MAP_SCALE
	width, height            int
}

func newGeoref(l *Label) georef {
	return georef{
		sampleOffset: l.SampleOffset,
		lineOffset:   l.LineOffset,
		centerLon:    l.CenterLongitude,
		pxPerRadLon:  float64(l.RadiusMeters*math.Cos(l.CenterLatitude*radians)) / l.MapScale,
		pxPerRadLat:  l.RadiusMeters / l.MapScale,
		width:        l.Samples,
		height:       l.Lines,
	}
}

// toPixel maps a geographic point to fractional pixel coordinates.
func (g georef) toPixel(c Coordinate) (x, y float64) {
	x = g.sampleOffset + float64(longitudeDelta(g.centerLon, c.LongitudeEast)*radians*g.pxPerRadLon) - pixelIndexShift
	y = g.lineOffset - float64(c.Latitude*radians*g.pxPerRadLat) - pixelIndexShift
	return x, y
}

// toGeographic is the inverse of toPixel.
func (g georef) toGeographic(x, y float64) Coordinate {
	lon := g.centerLon + float64((x+pixelIndexShift-g.sampleOffset)/g.pxPerRadLon)/radians
	lat := float64((g.lineOffset-y-pixelIndexShift)/g.pxPerRadLat) / radians
	return Coordinate{Latitude: lat, LongitudeEast: normalizeLongitude(lon)}
}
