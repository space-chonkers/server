package marsgeo

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// HiRISEProvider is the payload's source.provider for HiRISE products.
const HiRISEProvider = "NASA/JPL/University of Arizona (HiRISE)"

//go:embed registry.json
var embeddedRegistry []byte

// Registry lists the verified products the engine may draw from and, for
// each, the lattice of centres whose whole map footprint is valid. It is
// produced offline by cmd/marsgeo-prep.
type Registry struct {
	RegistryVersion string  `json:"registryVersion"`
	Entries         []Entry `json:"entries"`
}

// Entry is one product: identity, provenance, the label parameters the
// lattice was computed with, and the lattice itself.
type Entry struct {
	ProductID        string     `json:"productId"`
	ProductVersion   string     `json:"productVersion"`
	DatasetID        string     `json:"datasetId"`
	Provider         string     `json:"provider"`
	SourceURL        string     `json:"sourceUrl"`
	File             string     `json:"file"`
	SHA256           string     `json:"sha256"`
	ByteSize         int64      `json:"byteSize"`
	ResolutionMeters float64    `json:"resolutionMeters"`
	RadiusMeters     float64    `json:"radiusMeters"`
	Projection       Projection `json:"projection"`
	Lines            int        `json:"lines"`
	Samples          int        `json:"samples"`
	Kernel           int        `json:"kernel"`
	CellSizeMeters   float64    `json:"cellSizeMeters"`
	GridColumns      int        `json:"gridColumns"`
	GridRows         int        `json:"gridRows"`
	ValidCentres     Lattice    `json:"validCentres"`
}

// Projection duplicates the label's projection parameters so a changed or
// swapped file is caught at open.
type Projection struct {
	CenterLatitude  float64 `json:"centerLatitude"`
	CenterLongitude float64 `json:"centerLongitude"`
	LineOffset      float64 `json:"lineOffset"`
	SampleOffset    float64 `json:"sampleOffset"`
}

// DefaultRegistry decodes the embedded production registry.
func DefaultRegistry() (*Registry, error) { return LoadRegistry(embeddedRegistry) }

// LoadRegistry decodes and checks a registry document. Unknown fields, a
// kernel that does not match the product, an unsupported grid or a malformed
// lattice are errors.
func LoadRegistry(data []byte) (*Registry, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var r Registry
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	if strings.TrimSpace(r.RegistryVersion) == "" {
		return nil, fmt.Errorf("registry: registryVersion is required")
	}
	for i := range r.Entries {
		e := &r.Entries[i]
		if err := e.check(); err != nil {
			return nil, fmt.Errorf("registry entry %d (%s): %w", i, e.ProductID, err)
		}
	}
	return &r, nil
}

func (e *Entry) check() error {
	switch {
	case !validProductID(e.ProductID):
		return fmt.Errorf("productId %q does not match %s", e.ProductID, productIDPattern)
	case e.DatasetID != HiRISEDTMDatasetID || e.Provider != HiRISEProvider:
		return fmt.Errorf("dataset %q / provider %q not supported", e.DatasetID, e.Provider)
	case e.File == "" || e.File == "." || e.File == ".." || filepath.Base(e.File) != e.File:
		return fmt.Errorf("file %q must be a bare file name", e.File)
	case len(e.SHA256) != 64:
		return fmt.Errorf("sha256 must be 64 hex digits")
	case e.ResolutionMeters <= 0 || e.RadiusMeters <= 0 || e.ByteSize <= 0:
		return fmt.Errorf("resolution, radius and size must be positive")
	case e.GridColumns != DefaultGrid.Columns || e.GridRows != DefaultGrid.Rows || e.CellSizeMeters != DefaultGrid.CellSizeMeters:
		return fmt.Errorf("grid %dx%d @ %v m is not the supported %dx%d @ %v m", e.GridColumns, e.GridRows, e.CellSizeMeters, DefaultGrid.Columns, DefaultGrid.Rows, DefaultGrid.CellSizeMeters)
	case e.Kernel != KernelSize(e.CellSizeMeters, e.ResolutionMeters):
		return fmt.Errorf("kernel %d != ceil(%v / %v)", e.Kernel, e.CellSizeMeters, e.ResolutionMeters)
	}
	if err := e.ValidCentres.validate(); err != nil {
		return err
	}
	if e.ValidCentres.Count() == 0 {
		return fmt.Errorf("no valid centres")
	}
	return nil
}

// Terrain is an opened registry: every product file verified and held open.
// It is safe for concurrent use; Build only reads.
type Terrain struct {
	reg      *Registry
	products []*Product
	files    []*os.File
}

// Open verifies every registry product in dataDir: the file exists, its size
// and SHA-256 match, its label passes verification, and the label's
// parameters equal the registry's copy. Any failure is ErrDataUnavailable and
// names the product. There is no fallback.
func (r *Registry) Open(dataDir string) (*Terrain, error) {
	if len(r.Entries) == 0 {
		return nil, fmt.Errorf("%w: registry %s lists no products", ErrDataUnavailable, r.RegistryVersion)
	}
	t := &Terrain{reg: r}
	for i := range r.Entries {
		prod, f, err := openEntry(dataDir, &r.Entries[i])
		if err != nil {
			t.Close()
			return nil, fmt.Errorf("%w: %s: %v", ErrDataUnavailable, r.Entries[i].ProductID, err)
		}
		t.products = append(t.products, prod)
		t.files = append(t.files, f)
	}
	return t, nil
}

func openEntry(dataDir string, e *Entry) (*Product, *os.File, error) {
	if dataDir == "" {
		return nil, nil, fmt.Errorf("data directory not configured")
	}
	f, err := os.Open(filepath.Join(dataDir, e.File))
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*Product, *os.File, error) { f.Close(); return nil, nil, err }
	st, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if st.Size() != e.ByteSize {
		return fail(fmt.Errorf("size %d, registry says %d", st.Size(), e.ByteSize))
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, st.Size())); err != nil {
		return fail(err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != e.SHA256 {
		return fail(fmt.Errorf("sha256 %s, registry says %s", got, e.SHA256))
	}
	prod, err := OpenProduct(f, st.Size())
	if err != nil {
		return fail(err)
	}
	l := prod.Label
	got := Projection{CenterLatitude: l.CenterLatitude, CenterLongitude: l.CenterLongitude, LineOffset: l.LineOffset, SampleOffset: l.SampleOffset}
	if l.ProductID != e.ProductID || l.MapScale != e.ResolutionMeters || l.RadiusMeters != e.RadiusMeters || got != e.Projection || l.Lines != e.Lines || l.Samples != e.Samples || l.DatasetID != e.DatasetID {
		return fail(fmt.Errorf("label parameters differ from the registry"))
	}
	return prod, f, nil
}

// Close releases the product files and returns every close error, joined.
// Calling it again is a no-op.
func (t *Terrain) Close() error {
	var errs []error
	for _, f := range t.files {
		if err := f.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	t.files = nil
	return errors.Join(errs...)
}

// RegistryVersion identifies the registry content; a change can remap seeds.
func (t *Terrain) RegistryVersion() string { return t.reg.RegistryVersion }

// Location is the product and map centre a seed selects.
type Location struct {
	ProductID string
	PixelX    int
	PixelY    int
	Center    Coordinate
}

// Locate returns the location a seed selects, without sampling.
func (t *Terrain) Locate(seed uint32) Location {
	_, loc := t.locate(seed)
	return loc
}

func (t *Terrain) locate(seed uint32) (int, Location) {
	counts := make([]int, len(t.reg.Entries))
	for i := range t.reg.Entries {
		counts[i] = t.reg.Entries[i].ValidCentres.Count()
	}
	ei, ci := selectLocation(seed, counts)
	e := &t.reg.Entries[ei]
	x, y, _ := e.ValidCentres.Pixel(ci)
	return ei, Location{ProductID: e.ProductID, PixelX: x, PixelY: y, Center: t.products[ei].geo.toGeographic(float64(x), float64(y))}
}

// Build returns the MarsTerrainDataV1 payload for seed: a deterministic
// location from the registry, 51 x 51 area-averaged corner samples of real
// HiRISE elevation, validated before it is returned. It never returns a
// partial or substituted payload.
func (t *Terrain) Build(seed uint32) (MarsTerrainDataV1, error) {
	ei, loc := t.locate(seed)
	e, prod := &t.reg.Entries[ei], t.products[ei]
	grid := Grid{Columns: e.GridColumns, Rows: e.GridRows, CellSizeMeters: e.CellSizeMeters}

	rect, err := FootprintRect(prod.geo, loc.Center, grid, e.Kernel)
	if err != nil {
		return MarsTerrainDataV1{}, err
	}
	if !rect.inside(prod.Label.Samples, prod.Label.Lines) {
		return MarsTerrainDataV1{}, fmt.Errorf("%w: footprint %+v of %s leaves the raster", ErrNoCoverage, rect, e.ProductID)
	}
	win, err := prod.ReadWindow(rect.X0, rect.Y0, rect.Width(), rect.Height())
	if err != nil {
		return MarsTerrainDataV1{}, err
	}
	// Runtime re-check: every pixel the stencils can touch, from the bytes just read.
	if n := invalidInWindow(win, rect, prod.validity()); n > 0 {
		return MarsTerrainDataV1{}, fmt.Errorf("%w: %d invalid pixels in the footprint of %s at (%d, %d)", ErrNoCoverage, n, e.ProductID, loc.PixelX, loc.PixelY)
	}
	src, err := NewDTMSource(prod, win, e.Kernel, grid.CellSizeMeters)
	if err != nil {
		return MarsTerrainDataV1{}, err
	}
	samples, err := Generate(loc.Center, grid, src)
	if err != nil {
		return MarsTerrainDataV1{}, err
	}
	productID := e.ProductID
	d := MarsTerrainDataV1{
		Version: ContractVersion,
		Center:  loc.Center,
		Grid:    grid,
		Elevation: ElevationSamples{
			Sampling:      SamplingCellCorners,
			Columns:       grid.Columns + 1,
			Rows:          grid.Rows + 1,
			SamplesMeters: samples,
			Datum:         Datum,
		},
		Source: Source{DatasetID: e.DatasetID, Provider: e.Provider, ResolutionMeters: e.ResolutionMeters, ProductID: &productID},
		Seed:   seed,
	}
	if err := CheckShape(&d); err != nil {
		return MarsTerrainDataV1{}, err
	}
	return d, nil
}

// NewEntry describes an opened product for the registry. The lattice must
// have been scanned from the same product with the default grid.
func NewEntry(prod *Product, file, sha256Hex string, size int64, sourceURL string, lattice *Lattice) Entry {
	l := prod.Label
	return Entry{
		ProductID:        l.ProductID,
		ProductVersion:   l.ProductVersionID,
		DatasetID:        l.DatasetID,
		Provider:         HiRISEProvider,
		SourceURL:        sourceURL,
		File:             file,
		SHA256:           sha256Hex,
		ByteSize:         size,
		ResolutionMeters: l.MapScale,
		RadiusMeters:     l.RadiusMeters,
		Projection:       Projection{CenterLatitude: l.CenterLatitude, CenterLongitude: l.CenterLongitude, LineOffset: l.LineOffset, SampleOffset: l.SampleOffset},
		Lines:            l.Lines,
		Samples:          l.Samples,
		Kernel:           KernelSize(DefaultGrid.CellSizeMeters, l.MapScale),
		CellSizeMeters:   DefaultGrid.CellSizeMeters,
		GridColumns:      DefaultGrid.Columns,
		GridRows:         DefaultGrid.Rows,
		ValidCentres:     *lattice,
	}
}
