package marsgeo

import (
	"fmt"
	"math"
)

// ContractVersion is the MarsTerrainDataV1 version this package produces.
const ContractVersion = 1

// Datum names the vertical reference of every elevation this package emits:
// metres above the Mars 2000 areoid (the MOLA equipotential surface, Smith et
// al. 2001), the surface every HiRISE DTEE* label names. No conversion is done.
const Datum = "mars-2000-areoid"

// SamplingCellCorners is the only sampling scheme of contract v1.
const SamplingCellCorners = "cell-corners"

// MarsTerrainDataV1 mirrors the client contract (client commit 147863f,
// src/terrain-kit/marsTerrainContract.ts). Field names and JSON tags are the
// contract; do not rename them.
type MarsTerrainDataV1 struct {
	Version   int              `json:"version"`
	Center    Coordinate       `json:"center"`
	Grid      Grid             `json:"grid"`
	Elevation ElevationSamples `json:"elevation"`
	Source    Source           `json:"source"`
	Seed      uint32           `json:"seed"`
}

// Coordinate is a planetocentric latitude and east-positive longitude, degrees.
type Coordinate struct {
	Latitude      float64 `json:"latitude"`
	LongitudeEast float64 `json:"longitudeEast"`
}

// ElevationSamples holds the corner samples, row-major from the north-west
// corner, metres above Datum.
type ElevationSamples struct {
	Sampling      string    `json:"sampling"`
	Columns       int       `json:"columns"`
	Rows          int       `json:"rows"`
	SamplesMeters []float64 `json:"samplesMeters"`
	Datum         string    `json:"datum"`
}

// Source is the payload's provenance block.
type Source struct {
	DatasetID        string  `json:"datasetId"`
	Provider         string  `json:"provider"`
	ResolutionMeters float64 `json:"resolutionMeters"`
	// ProductID is omitted, never sent as null, when there is none.
	ProductID *string `json:"productId,omitempty"`
}

// CheckShape enforces the two cross-field rules JSON Schema cannot express
// (contract §7) and, as a last line of defence before a payload leaves this
// package, rejects any sample that is non-finite or whose float32 bit pattern
// is an ISIS special value (which includes the HiRISE MISSING_CONSTANT).
func CheckShape(d *MarsTerrainDataV1) error {
	g, e := d.Grid, d.Elevation
	if g.Columns < 1 || g.Rows < 1 {
		return fmt.Errorf("grid must have at least 1x1 cells, got %dx%d", g.Columns, g.Rows)
	}
	if e.Columns != g.Columns+1 || e.Rows != g.Rows+1 {
		return fmt.Errorf("elevation must be %dx%d corner samples, got %dx%d", g.Columns+1, g.Rows+1, e.Columns, e.Rows)
	}
	if len(e.SamplesMeters) != e.Columns*e.Rows {
		return fmt.Errorf("elevation.samplesMeters has %d values, expected %d", len(e.SamplesMeters), e.Columns*e.Rows)
	}
	for i, v := range e.SamplesMeters {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("elevation sample %d (row %d, column %d) is not finite", i, i/e.Columns, i%e.Columns)
		}
		if isISISSpecialBits(math.Float32bits(float32(v))) && float64(float32(v)) == v {
			return fmt.Errorf("elevation sample %d (row %d, column %d) is an ISIS special value", i, i/e.Columns, i%e.Columns)
		}
	}
	return nil
}
