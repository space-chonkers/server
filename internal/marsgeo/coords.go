package marsgeo

import (
	"errors"
	"fmt"
	"math"
)

// MarsMeanRadiusMeters is the sphere radius of the client's horizontal
// convention (contract §3, MARS_MEAN_RADIUS_METERS). It places corners and
// kernel sub-samples (local <-> geographic) and nothing else. A product's own
// projection radius (Product.RadiusMeters) is a different number and is used
// only for geographic -> pixel.
const MarsMeanRadiusMeters = 3_389_500

// MaxMapLatitudeDegrees is the highest |latitude| a map centre may have.
const MaxMapLatitudeDegrees = 85

// ErrInvalidCoordinate reports a coordinate the local mapping cannot represent.
var ErrInvalidCoordinate = errors.New("marsgeo: invalid coordinate")

// LocalPoint is metres east and north of a map centre.
type LocalPoint struct {
	East, North float64
}

// Runtime float64 values, not constants: Go folds constant expressions
// exactly, while the client (JavaScript) rounds after every operation, so
// these follow Math.PI / 180 and the client's evaluation order. The formulas
// and order agree with marsCoordinates.ts, but results are not guaranteed
// bit-identical: Go's math.Cos and a JS engine's Math.cos may differ by an ulp
// or two (and JS engines differ from each other). An independent comparison
// against the client code found differences of at most 2 ulp, about 1e-14 m.
var (
	piFloat64    float64 = math.Pi
	radians              = piFloat64 / 180
	clientRadius float64 = MarsMeanRadiusMeters
)

func checkMapCenter(c Coordinate) error {
	if math.IsNaN(c.Latitude) || math.IsInf(c.Latitude, 0) || math.Abs(c.Latitude) > MaxMapLatitudeDegrees {
		return fmt.Errorf("%w: map centre latitude %v outside [-%d, %d]", ErrInvalidCoordinate, c.Latitude, MaxMapLatitudeDegrees, MaxMapLatitudeDegrees)
	}
	if math.IsNaN(c.LongitudeEast) || math.IsInf(c.LongitudeEast, 0) {
		return fmt.Errorf("%w: map centre longitude %v not finite", ErrInvalidCoordinate, c.LongitudeEast)
	}
	return nil
}

// jsMod is JavaScript's % operator; math.Mod truncates the same way.
func jsMod(a, b float64) float64 { return math.Mod(a, b) }

// longitudeDelta is the signed shortest difference to-from in [-180, 180),
// ported verbatim from marsCoordinates.ts.
func longitudeDelta(from, to float64) float64 {
	return jsMod(jsMod(to-from, 360)+540, 360) - 180
}

// normalizeLongitude maps any finite longitude into [0, 360).
func normalizeLongitude(lon float64) float64 {
	return jsMod(jsMod(lon, 360)+360, 360)
}

// GeographicToLocal is the client's geographicToLocal (contract §3).
func GeographicToLocal(p, center Coordinate) (LocalPoint, error) {
	if err := checkMapCenter(center); err != nil {
		return LocalPoint{}, err
	}
	if math.IsNaN(p.Latitude) || math.IsInf(p.Latitude, 0) || math.Abs(p.Latitude) > 90 || math.IsNaN(p.LongitudeEast) || math.IsInf(p.LongitudeEast, 0) {
		return LocalPoint{}, fmt.Errorf("%w: (%v, %v)", ErrInvalidCoordinate, p.Latitude, p.LongitudeEast)
	}
	return LocalPoint{
		East:  longitudeDelta(center.LongitudeEast, p.LongitudeEast) * radians * clientRadius * math.Cos(center.Latitude*radians),
		North: (p.Latitude - center.Latitude) * radians * clientRadius,
	}, nil
}

// LocalToGeographic is the client's localToGeographic (contract §3):
//
//	latitude  = φ0 + north / (R·π/180)
//	longitude = (λ0 + east / (R·cos φ0·π/180)) mod 360
func LocalToGeographic(l LocalPoint, center Coordinate) (Coordinate, error) {
	if err := checkMapCenter(center); err != nil {
		return Coordinate{}, err
	}
	if math.IsNaN(l.East) || math.IsInf(l.East, 0) || math.IsNaN(l.North) || math.IsInf(l.North, 0) {
		return Coordinate{}, fmt.Errorf("%w: local point (%v, %v)", ErrInvalidCoordinate, l.East, l.North)
	}
	// Explicit float64 conversions stop the compiler fusing a*b+c into FMA.
	lon := center.LongitudeEast + float64(l.East/(radians*clientRadius*math.Cos(center.Latitude*radians)))
	lat := center.Latitude + float64(l.North/(radians*clientRadius))
	if math.Abs(lat) > 90 {
		return Coordinate{}, fmt.Errorf("%w: latitude %v beyond the pole", ErrInvalidCoordinate, lat)
	}
	return Coordinate{Latitude: lat, LongitudeEast: normalizeLongitude(lon)}, nil
}
