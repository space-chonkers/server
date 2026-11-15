package marsgeo

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestLocalGeographicFormulas(t *testing.T) {
	center := Coordinate{Latitude: -4.5, LongitudeEast: 137.4}
	// 250 m north: Δφ = 250 / (R·π/180) degrees.
	geo, err := LocalToGeographic(LocalPoint{North: 250}, center)
	if err != nil {
		t.Fatal(err)
	}
	if want := -4.5 + 250/(MarsMeanRadiusMeters*math.Pi/180); math.Abs(geo.Latitude-want) > 1e-12 || math.Abs(geo.LongitudeEast-137.4) > 1e-12 {
		t.Fatalf("north: got %+v want lat %v", geo, want)
	}
	geo, _ = LocalToGeographic(LocalPoint{East: 250}, center)
	if want := 137.4 + 250/(MarsMeanRadiusMeters*math.Pi/180*math.Cos(-4.5*math.Pi/180)); math.Abs(geo.LongitudeEast-want) > 1e-12 {
		t.Fatalf("east: got %v want %v", geo.LongitudeEast, want)
	}
}

func TestLocalGeographicRoundTrip(t *testing.T) {
	for _, c := range []Coordinate{{-4.5, 137.4}, {85, 10}, {-85, 359.9999}, {0, 0}, {17.8, 232.49}} {
		for _, p := range []LocalPoint{{0, 0}, {250, -250}, {-250, 250}, {-4.123, 3.21}} {
			g, err := LocalToGeographic(p, c)
			if err != nil {
				t.Fatal(err)
			}
			back, err := GeographicToLocal(g, c)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(back.East-p.East) > 1e-6 || math.Abs(back.North-p.North) > 1e-6 {
				t.Errorf("centre %+v point %+v: round trip %+v", c, p, back)
			}
			g2, _ := LocalToGeographic(back, c)
			if math.Abs(g2.Latitude-g.Latitude) > 1e-9 || math.Abs(longitudeDelta(g.LongitudeEast, g2.LongitudeEast)) > 1e-9 {
				t.Errorf("geo round trip %+v vs %+v", g, g2)
			}
		}
	}
}

func TestLongitudeWraparound(t *testing.T) {
	c := Coordinate{Latitude: 0, LongitudeEast: 359.999}
	g, err := LocalToGeographic(LocalPoint{East: 250}, c)
	if err != nil {
		t.Fatal(err)
	}
	if g.LongitudeEast < 0 || g.LongitudeEast >= 0.01 {
		t.Fatalf("east of 359.999 should wrap to just above 0, got %v", g.LongitudeEast)
	}
	l, _ := GeographicToLocal(g, c)
	if l.East <= 0 || math.Abs(l.East-250) > 1e-6 {
		t.Fatalf("wrapped point should be 250 m east, got %v", l.East)
	}
	g, _ = LocalToGeographic(LocalPoint{East: -250}, Coordinate{0, 0.001})
	if g.LongitudeEast < 359.99 || g.LongitudeEast >= 360 {
		t.Fatalf("west of 0.001 should wrap below 360, got %v", g.LongitudeEast)
	}
	if d := longitudeDelta(0, 180); d != -180 {
		t.Fatalf("longitudeDelta(0,180) = %v, want -180", d)
	}
	if d := longitudeDelta(10, 350); d != -20 {
		t.Fatalf("longitudeDelta(10,350) = %v, want -20", d)
	}
}

func TestCoordinateErrors(t *testing.T) {
	if _, err := LocalToGeographic(LocalPoint{}, Coordinate{Latitude: 85}); err != nil {
		t.Fatalf("85 must be accepted: %v", err)
	}
	bad := []Coordinate{{85.0001, 0}, {-85.0001, 0}, {math.NaN(), 0}, {0, math.Inf(1)}}
	for _, c := range bad {
		if _, err := LocalToGeographic(LocalPoint{}, c); !errors.Is(err, ErrInvalidCoordinate) {
			t.Errorf("centre %+v: err = %v", c, err)
		}
	}
	if _, err := GeographicToLocal(Coordinate{90.5, 0}, Coordinate{}); !errors.Is(err, ErrInvalidCoordinate) {
		t.Errorf("point lat > 90 accepted")
	}
	if _, err := LocalToGeographic(LocalPoint{East: math.NaN()}, Coordinate{}); !errors.Is(err, ErrInvalidCoordinate) {
		t.Errorf("NaN local accepted")
	}
}

func TestCornerLocal(t *testing.T) {
	g := DefaultGrid
	if p := g.CornerLocal(0, 0); p != (LocalPoint{-250, 250}) {
		t.Fatalf("NW corner = %+v", p)
	}
	if p := g.CornerLocal(50, 50); p != (LocalPoint{250, -250}) {
		t.Fatalf("SE corner = %+v", p)
	}
	if p := g.CornerLocal(25, 25); p != (LocalPoint{0, 0}) {
		t.Fatalf("centre corner = %+v", p)
	}
}

// planeSource is the client's synthetic generator: -4000 + 2·col - 3·row,
// recovered from the local corner position.
type planeSource struct{ g Grid }

func (s planeSource) SampleCorner(_ Coordinate, p LocalPoint, cell float64) (float64, error) {
	col := p.East/cell + float64(s.g.Columns)/2
	row := float64(s.g.Rows)/2 - p.North/cell
	return -4000 + 2*col - 3*row, nil
}

func TestGenerateReproducesClientFixtures(t *testing.T) {
	for _, tc := range []struct {
		file string
		grid Grid
	}{
		{"synthetic_2x2.v1.json", Grid{2, 2, 10}},
		{"synthetic_50x50.v1.json", DefaultGrid},
	} {
		t.Run(tc.file, func(t *testing.T) {
			var want MarsTerrainDataV1
			if err := json.Unmarshal(readContractFile(t, tc.file), &want); err != nil {
				t.Fatal(err)
			}
			center := Coordinate{Latitude: 10, LongitudeEast: 20}
			samples, err := Generate(center, tc.grid, planeSource{tc.grid})
			if err != nil {
				t.Fatal(err)
			}
			got := MarsTerrainDataV1{
				Version:   ContractVersion,
				Center:    center,
				Grid:      tc.grid,
				Elevation: ElevationSamples{Sampling: SamplingCellCorners, Columns: tc.grid.Columns + 1, Rows: tc.grid.Rows + 1, SamplesMeters: samples, Datum: want.Elevation.Datum},
				Source:    want.Source,
				Seed:      1234,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("generated payload differs from fixture")
			}
			gb, _ := json.Marshal(&got)
			wb, _ := json.Marshal(&want)
			if string(gb) != string(wb) {
				t.Fatal("encoded bytes differ")
			}
			mustValidPayload(t, &got)
			n := tc.grid.Columns + 1
			// Orientation: sample 0 is NW; values rise east, fall south; index = row·C + col.
			if samples[0] != -4000 || samples[1] != -3998 || samples[n] != -4003 {
				t.Fatalf("orientation wrong: %v %v %v", samples[0], samples[1], samples[n])
			}
			last := float64(-4000 + 2*tc.grid.Columns - 3*tc.grid.Rows)
			if samples[len(samples)-1] != last {
				t.Fatalf("last sample = %v want %v", samples[len(samples)-1], last)
			}
		})
	}
}

type failingSource struct{ after int }

func (f *failingSource) SampleCorner(Coordinate, LocalPoint, float64) (float64, error) {
	if f.after == 0 {
		return 0, ErrNoCoverage
	}
	f.after--
	return 1, nil
}

func TestGenerateNoPartialOutput(t *testing.T) {
	samples, err := Generate(Coordinate{}, DefaultGrid, &failingSource{after: 100})
	if !errors.Is(err, ErrNoCoverage) || samples != nil {
		t.Fatalf("got %d samples, err %v", len(samples), err)
	}
}
