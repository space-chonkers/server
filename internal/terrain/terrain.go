// Package terrain serves real Mars terrain at GET /v1/mars-terrain/{seed}.
//
// catalog.json lists regional heightmaps prepared offline from HiRISE DTMs
// (prepare_region.py; see README.md). Each region is a north-up grid of
// float32 elevations 10 m apart. A seed picks a region, then a 500 m × 500 m
// window in it, and the response is that window's 51 × 51 heights as a
// MarsTerrainDataV1 payload. Nothing is resampled or reprojected at runtime.
package terrain

import (
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Route is the mux pattern for Catalog.ServeHTTP.
const Route = "GET /v1/mars-terrain/{seed}"

const (
	cells   = 50        // map cells per side
	samples = cells + 1 // corner samples per side
	spacing = 10.0      // metres between samples, which is also the cell size
)

//go:embed catalog.json *.f32
var embedded embed.FS

// Data is the MarsTerrainDataV1 payload (client: src/terrain-kit/marsTerrainContract.ts).
type Data struct {
	Version   int       `json:"version"`
	Center    Center    `json:"center"`
	Grid      Grid      `json:"grid"`
	Elevation Elevation `json:"elevation"`
	Source    Source    `json:"source"`
	Seed      uint32    `json:"seed"`
}

type Center struct {
	Latitude      float64 `json:"latitude"`
	LongitudeEast float64 `json:"longitudeEast"`
}

type Grid struct {
	Columns        int     `json:"columns"`
	Rows           int     `json:"rows"`
	CellSizeMeters float64 `json:"cellSizeMeters"`
}

type Elevation struct {
	Sampling      string    `json:"sampling"`
	Columns       int       `json:"columns"`
	Rows          int       `json:"rows"`
	SamplesMeters []float32 `json:"samplesMeters"` // row-major, north-west first
	Datum         string    `json:"datum"`
}

type Source struct {
	DatasetID        string  `json:"datasetId"`
	Provider         string  `json:"provider"`
	ResolutionMeters float64 `json:"resolutionMeters"`
	ProductID        string  `json:"productId,omitempty"`
}

// region is one prepared heightmap from catalog.json.
type region struct {
	ID                     string  `json:"id"`
	File                   string  `json:"file"`
	SHA256                 string  `json:"sha256"`
	Columns                int     `json:"columns"`
	Rows                   int     `json:"rows"`
	SpacingMeters          float64 `json:"spacingMeters"`
	NorthWestLatitude      float64 `json:"northWestLatitude"` // of sample (row 0, column 0)
	NorthWestLongitudeEast float64 `json:"northWestLongitudeEast"`
	LatitudeStepDegrees    float64 `json:"latitudeStepDegrees"`  // per row, southwards
	LongitudeStepDegrees   float64 `json:"longitudeStepDegrees"` // per column, eastwards
	Datum                  string  `json:"datum"`
	Source                 Source  `json:"source"`

	heights []byte // little-endian float32, row-major from the north-west
}

// windows is the number of 51 × 51 sample windows that fit in the region.
func (r *region) windows() uint64 {
	return uint64(r.Columns-cells) * uint64(r.Rows-cells)
}

func (r *region) height(row, col int) float32 {
	i := 4 * (row*r.Columns + col)
	return math.Float32frombits(binary.LittleEndian.Uint32(r.heights[i:]))
}

// Catalog holds the regions and serves Route. A nil *Catalog answers 503.
type Catalog struct {
	Version string   `json:"version"`
	Regions []region `json:"regions"`
}

// Load reads and checks the embedded catalog.
func Load() (*Catalog, error) { return load(embedded) }

func load(fsys fs.FS) (*Catalog, error) {
	raw, err := fs.ReadFile(fsys, "catalog.json")
	if err != nil {
		return nil, fmt.Errorf("terrain: %w", err)
	}
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("terrain: catalog.json: %w", err)
	}
	if len(c.Regions) == 0 {
		return nil, errors.New("terrain: catalog has no regions")
	}
	for i := range c.Regions {
		r := &c.Regions[i]
		if err := r.load(fsys); err != nil {
			return nil, fmt.Errorf("terrain: region %q: %w", r.ID, err)
		}
	}
	return &c, nil
}

func (r *region) load(fsys fs.FS) error {
	south := r.NorthWestLatitude - float64(r.Rows-1)*r.LatitudeStepDegrees
	if r.Columns < samples || r.Rows < samples || r.SpacingMeters != spacing ||
		r.LatitudeStepDegrees <= 0 || r.LongitudeStepDegrees <= 0 ||
		math.Abs(r.NorthWestLatitude) > 85 || math.Abs(south) > 85 {
		return errors.New("bad grid: need at least 51 × 51 samples, 10 m apart, within ±85° latitude")
	}
	if s := r.Source; r.Datum != "mars-2000-areoid" || strings.TrimSpace(s.DatasetID) == "" ||
		strings.TrimSpace(s.Provider) == "" || !(s.ResolutionMeters > 0) {
		return errors.New("bad metadata: need datum mars-2000-areoid, a dataset ID, a provider and a positive resolution")
	}
	var err error
	if r.heights, err = fs.ReadFile(fsys, r.File); err != nil {
		return err
	}
	if len(r.heights) != 4*r.Columns*r.Rows {
		return fmt.Errorf("%s has %d bytes, want %d", r.File, len(r.heights), 4*r.Columns*r.Rows)
	}
	if sum := sha256.Sum256(r.heights); hex.EncodeToString(sum[:]) != r.SHA256 {
		return fmt.Errorf("%s SHA-256 mismatch", r.File)
	}
	for row := range r.Rows {
		for col := range r.Columns {
			if v := float64(r.height(row, col)); math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("%s: invalid height at row %d, column %d", r.File, row, col)
			}
		}
	}
	return nil
}

// Terrain returns the map for seed. The same seed and catalog give the same map.
// The seed picks a region uniformly, so a large region cannot crowd out the
// others, then a window uniformly within that region.
func (c *Catalog) Terrain(seed uint32) Data {
	h := mix(uint64(seed))
	r := &c.Regions[h%uint64(len(c.Regions))]
	n := mix(h) % r.windows() // windows are numbered by top row, then left column
	top, left := int(n/uint64(r.Columns-cells)), int(n%uint64(r.Columns-cells))

	heights := make([]float32, 0, samples*samples)
	for row := top; row < top+samples; row++ {
		for col := left; col < left+samples; col++ {
			heights = append(heights, r.height(row, col))
		}
	}
	return Data{
		Version: 1,
		Center: Center{
			Latitude:      r.NorthWestLatitude - float64(top+cells/2)*r.LatitudeStepDegrees,
			LongitudeEast: normalizeLongitude(r.NorthWestLongitudeEast + float64(left+cells/2)*r.LongitudeStepDegrees),
		},
		Grid:      Grid{Columns: cells, Rows: cells, CellSizeMeters: spacing},
		Elevation: Elevation{Sampling: "cell-corners", Columns: samples, Rows: samples, SamplesMeters: heights, Datum: r.Datum},
		Source:    r.Source,
		Seed:      seed,
	}
}

// normalizeLongitude maps an east longitude into [0, 360), so a region that
// crosses the prime meridian still gives valid centres.
func normalizeLongitude(lon float64) float64 {
	lon = math.Mod(lon, 360)
	if lon < 0 {
		lon += 360
	}
	if lon >= 360 { // a tiny negative lon rounds up to exactly 360
		lon = 0
	}
	return lon
}

// mix spreads consecutive seeds across the catalog (the SplitMix64 finaliser).
func mix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	return x ^ x>>31
}

// ServeHTTP answers Route: 200 with the map, 400 for a seed that is not a
// canonical decimal uint32, and 503 if the catalog failed to load (c is nil).
func (c *Catalog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("seed")
	seed, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || strconv.FormatUint(seed, 10) != raw {
		http.Error(w, "malformed seed: want a decimal integer in [0, 4294967295]", http.StatusBadRequest)
		return
	}
	if c == nil {
		http.Error(w, "Mars terrain catalog unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c.Terrain(uint32(seed)))
}
