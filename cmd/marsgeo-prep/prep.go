package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/space-chonkers/server/internal/marsgeo"
)

const (
	userAgent    = "marsgeo-prep"
	labelProbe   = 64 << 10 // bytes fetched to find the label END
	credit       = "NASA/JPL/University of Arizona"
	usagePolicy  = "https://uahirise.org/media/usage.php"
	cropSuffix   = "_crop.IMG"
	provenanceFn = "_crop.provenance.json"
)

type config struct {
	url, out, crop, sourceSHA256 string
	registry, registryVersion    string
	latticeStepMeters            float64
	client                       *http.Client
	commandLine                  []string
	now                          func() time.Time
}

// Provenance documents a committed crop.
type Provenance struct {
	ProductID        string   `json:"productId"`
	ProductVersionID string   `json:"productVersionId"`
	DatasetID        string   `json:"datasetId"`
	SourceURL        string   `json:"sourceUrl"`
	SourceBytes      int64    `json:"sourceBytes"`
	SourceSHA256     string   `json:"sourceSha256"`
	SourceLabelSHA   string   `json:"sourceLabelSha256"`
	CropWindow       [4]int   `json:"cropWindow"`
	CropFile         string   `json:"cropFile"`
	CropBytes        int      `json:"cropBytes"`
	CropSHA256       string   `json:"cropSha256"`
	SoftwareName     string   `json:"softwareName"`
	DatumNote        string   `json:"datumNote"`
	Credit           string   `json:"credit"`
	UsagePolicy      string   `json:"usagePolicy"`
	Generated        string   `json:"generated"`
	CommandLine      []string `json:"commandLine"`
}

func run(cfg config) error {
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.crop == "" {
		return runFull(cfg)
	}
	return runCrop(cfg)
}

// fetchRange returns bytes [off, off+n) of url and the total file size.
func fetchRange(cfg config, off, n int64) ([]byte, int64, error) {
	req, err := http.NewRequest(http.MethodGet, cfg.url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
	resp, err := cfg.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return nil, 0, fmt.Errorf("range request: HTTP %s (the server must support Range)", resp.Status)
	}
	var start, end, total int64
	if _, err := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start != off {
		return nil, 0, fmt.Errorf("unexpected Content-Range %q", resp.Header.Get("Content-Range"))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, n+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(b)) != end-start+1 {
		return nil, 0, fmt.Errorf("short range body: %d bytes", len(b))
	}
	return b, total, nil
}

// fetchLabel reads and verifies the label at the start of the product.
func fetchLabel(cfg config) ([]byte, *marsgeo.Label, int64, error) {
	head, total, err := fetchRange(cfg, 0, labelProbe)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("fetch label: %w", err)
	}
	lab, err := marsgeo.ParseLabel(head)
	if err != nil {
		return nil, nil, 0, err
	}
	if need := lab.ImageOffset + lab.ImageBytes(); total < need {
		return nil, nil, 0, fmt.Errorf("%w: file has %d bytes, label declares %d", marsgeo.ErrInvalidProduct, total, need)
	}
	return head, lab, total, nil
}

func latticeStep(cfg config, mapScale float64) (int, error) {
	step := int(math.Round(cfg.latticeStepMeters / mapScale))
	if step < 1 {
		return 0, fmt.Errorf("lattice step %v m is below one pixel", cfg.latticeStepMeters)
	}
	return step, nil
}

func runFull(cfg config) error {
	if cfg.sourceSHA256 != "" {
		return errors.New("-source-sha256 is only for crop mode; the full download is hashed directly")
	}
	_, lab, total, err := fetchLabel(cfg)
	if err != nil {
		return err
	}
	name := lab.ProductID + ".IMG"
	tmp, err := os.CreateTemp(cfg.out, ".marsgeo-prep-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	req, err := http.NewRequest(http.MethodGet, cfg.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := cfg.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength != total {
		return fmt.Errorf("download: HTTP %s, %d bytes, expected %d", resp.Status, resp.ContentLength, total)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if n != total {
		return fmt.Errorf("download: got %d bytes, expected %d", n, total)
	}
	sum := hex.EncodeToString(h.Sum(nil))

	prod, err := marsgeo.OpenProduct(tmp, n)
	if err != nil {
		return err
	}
	if prod.Label.ProductID != lab.ProductID {
		return errors.New("label changed during download")
	}
	reg, err := buildRegistry(cfg, prod, name, sum, n)
	if err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(cfg.out, name)); err != nil {
		return err
	}
	return writeJSON(cfg.registry, reg)
}

func runCrop(cfg config) error {
	var x0, y0, w, h int
	if _, err := fmt.Sscanf(cfg.crop, "%d,%d,%d,%d", &x0, &y0, &w, &h); err != nil || w <= 0 || h <= 0 || x0 < 0 || y0 < 0 {
		return fmt.Errorf("-crop must be x0,y0,w,h with w, h > 0: %q", cfg.crop)
	}
	if len(cfg.sourceSHA256) != 64 {
		return errors.New("crop mode requires -source-sha256 (64 hex digits) of the full source file")
	}
	head, lab, total, err := fetchLabel(cfg)
	if err != nil {
		return err
	}
	if x0+w > lab.Samples || y0+h > lab.Lines {
		return fmt.Errorf("crop %s outside the %dx%d raster", cfg.crop, lab.Samples, lab.Lines)
	}
	// One Range request for the whole lines of the window.
	rb := int64(lab.RecordBytes)
	lines, _, err := fetchRange(cfg, lab.ImageOffset+int64(y0)*rb, int64(h)*rb)
	if err != nil {
		return fmt.Errorf("fetch lines: %w", err)
	}
	// Assemble a minimal in-memory product (label + the fetched lines) so the
	// library reads the window through its single decoding path.
	src, win, err := windowFromLines(head, lab, lines, x0, y0, w, h)
	if err != nil {
		return err
	}
	labelSum := sha256.Sum256(head[:lab.ImageOffset])
	comment := fmt.Sprintf("Crop of %s (version %s), source pixels x0=%d y0=%d w=%d h=%d (0-based), made by marsgeo-prep. Credit: %s. Usage policy: %s",
		lab.ProductID, lab.ProductVersionID, x0, y0, w, h, credit, usagePolicy)
	crop, err := marsgeo.CropProduct(head, src, win, comment)
	if err != nil {
		return err
	}
	cropName := lab.ProductID + cropSuffix
	cropSum := sha256.Sum256(crop)
	cropProd, err := marsgeo.OpenProduct(bytes.NewReader(crop), int64(len(crop)))
	if err != nil {
		return err
	}
	reg, err := buildRegistry(cfg, cropProd, cropName, hex.EncodeToString(cropSum[:]), int64(len(crop)))
	if err != nil {
		return err
	}
	prov := Provenance{
		ProductID: lab.ProductID, ProductVersionID: lab.ProductVersionID, DatasetID: lab.DatasetID,
		SourceURL: cfg.url, SourceBytes: total, SourceSHA256: strings.ToLower(cfg.sourceSHA256),
		SourceLabelSHA: hex.EncodeToString(labelSum[:]),
		CropWindow:     [4]int{x0, y0, w, h}, CropFile: cropName, CropBytes: len(crop),
		CropSHA256: hex.EncodeToString(cropSum[:]), SoftwareName: lab.SoftwareName,
		DatumNote: strings.Join(strings.Fields(lab.Note), " "), Credit: credit, UsagePolicy: usagePolicy,
		Generated: cfg.now().UTC().Format("2006-01-02"), CommandLine: cfg.commandLine,
	}
	if err := os.WriteFile(filepath.Join(cfg.out, cropName), crop, 0o644); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(cfg.out, lab.ProductID+provenanceFn), prov); err != nil {
		return err
	}
	return writeJSON(cfg.registry, reg)
}

// windowFromLines opens the source product over a sparse reader that holds
// only the label and the fetched lines (any other read fails), then reads the
// window in source pixel coordinates through the library's decoding path.
func windowFromLines(head []byte, lab *marsgeo.Label, lines []byte, x0, y0, w, h int) (*marsgeo.Product, *marsgeo.Window, error) {
	if lab.ImageOffset > int64(len(head)) {
		return nil, nil, fmt.Errorf("label region (%d bytes) exceeds the %d-byte probe", lab.ImageOffset, len(head))
	}
	r := &sparseImage{label: head[:lab.ImageOffset], imageOffset: lab.ImageOffset, linesOffset: lab.ImageOffset + int64(y0)*int64(lab.RecordBytes), lines: lines}
	size := lab.ImageOffset + lab.ImageBytes()
	src, err := marsgeo.OpenProduct(r, size)
	if err != nil {
		return nil, nil, err
	}
	win, err := src.ReadWindow(x0, y0, w, h)
	if err != nil {
		return nil, nil, err
	}
	return src, win, nil
}

// sparseImage serves the label and the fetched line range of a product; any
// read outside them is an error, so nothing unfetched is ever used.
type sparseImage struct {
	label       []byte
	imageOffset int64
	linesOffset int64
	lines       []byte
}

func (s *sparseImage) ReadAt(p []byte, off int64) (int, error) {
	if off < int64(len(s.label)) {
		n := copy(p, s.label[off:])
		if n < len(p) {
			return n, io.EOF // label probe reads past the label; that is fine
		}
		return n, nil
	}
	if off >= s.linesOffset && off+int64(len(p)) <= s.linesOffset+int64(len(s.lines)) {
		return copy(p, s.lines[off-s.linesOffset:]), nil
	}
	return 0, fmt.Errorf("read at %d (%d bytes) outside fetched lines", off, len(p))
}

func buildRegistry(cfg config, prod *marsgeo.Product, file, sum string, size int64) (*marsgeo.Registry, error) {
	step, err := latticeStep(cfg, prod.Label.MapScale)
	if err != nil {
		return nil, err
	}
	k := marsgeo.KernelSize(marsgeo.DefaultGrid.CellSizeMeters, prod.Label.MapScale)
	lat, err := marsgeo.ScanLattice(prod, marsgeo.DefaultGrid, k, step)
	if err != nil {
		return nil, err
	}
	if lat.Count() == 0 {
		return nil, fmt.Errorf("%w: %s has no valid %v m map centre", marsgeo.ErrNoCoverage, prod.Label.ProductID, marsgeo.DefaultGrid.CellSizeMeters*float64(marsgeo.DefaultGrid.Columns))
	}
	reg := &marsgeo.Registry{RegistryVersion: cfg.registryVersion, Entries: []marsgeo.Entry{marsgeo.NewEntry(prod, file, sum, size, cfg.url, lat)}}
	raw, err := json.Marshal(reg)
	if err != nil {
		return nil, err
	}
	if _, err := marsgeo.LoadRegistry(raw); err != nil {
		return nil, err
	}
	return reg, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
