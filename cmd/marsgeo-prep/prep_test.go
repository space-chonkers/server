package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/space-chonkers/server/internal/marsgeo"
)

// synthetic builds an n x n, 2 m HiRISE-like product (value = 1000·row + col)
// with pixel (n/2, n/2) at 17°N 232.49°E.
func synthetic(n int, sampleType string) []byte {
	lineOffset := 17*math.Pi/180*3394839.8133163/2 + float64(n/2)
	label := fmt.Sprintf(`PDS_VERSION_ID = PDS3
RECORD_TYPE = FIXED_LENGTH
RECORD_BYTES = %d
FILE_RECORDS = %d
^IMAGE = 2
DATA_SET_ID = "MRO-M-HIRISE-5-DTM-V1.0"
PRODUCT_ID = "DTEED_PREP_TEST_A01"
PRODUCT_VERSION_ID = "1"
SOFTWARE_NAME = "synthetic"
OBJECT = IMAGE
  LINES = %d
  LINE_SAMPLES = %d
  BANDS = 1
  OFFSET = 0.0
  SCALING_FACTOR = 1.0
  NOTE = "Synthetic. Elevations above the (Mars 2000 Datum)."
  SAMPLE_BITS = 32
  SAMPLE_TYPE = %s
  MISSING_CONSTANT = 16#FF7FFFFB#
  VALID_MINIMUM = 0
  VALID_MAXIMUM = 1000000
END_OBJECT = IMAGE
OBJECT = IMAGE_MAP_PROJECTION
  MAP_PROJECTION_TYPE = "EQUIRECTANGULAR"
  A_AXIS_RADIUS = 3394.8398133163 <KM>
  B_AXIS_RADIUS = 3394.8398133163 <KM>
  C_AXIS_RADIUS = 3394.8398133163 <KM>
  COORDINATE_SYSTEM_NAME = PLANETOCENTRIC
  POSITIVE_LONGITUDE_DIRECTION = EAST
  CENTER_LATITUDE = 17.0 <DEG>
  CENTER_LONGITUDE = 232.49 <DEG>
  LINE_FIRST_PIXEL = 1
  LINE_LAST_PIXEL = %d
  SAMPLE_FIRST_PIXEL = 1
  SAMPLE_LAST_PIXEL = %d
  MAP_PROJECTION_ROTATION = 0.0 <DEG>
  MAP_SCALE = 2.0 <METERS/PIXEL>
  MAXIMUM_LATITUDE = 18.0 <DEG>
  MINIMUM_LATITUDE = 16.0 <DEG>
  LINE_PROJECTION_OFFSET = %s <PIXEL>
  SAMPLE_PROJECTION_OFFSET = %d <PIXEL>
  EASTERNMOST_LONGITUDE = 232.6 <DEG>
  WESTERNMOST_LONGITUDE = 232.4 <DEG>
END_OBJECT = IMAGE_MAP_PROJECTION
END
`, n*4, n+1, n, n, sampleType, n, n, fmt.Sprintf("%.17g", lineOffset), n/2)
	label = strings.ReplaceAll(label, "\n", "\r\n")
	var b bytes.Buffer
	b.WriteString(label)
	b.Write(bytes.Repeat([]byte{' '}, n*4-len(label)))
	raw := make([]byte, 4)
	for i := 0; i < n*n; i++ {
		v := math.Float32bits(float32(1000*(i/n) + i%n))
		if i%n == 0 {
			v = 0xFF7FFFFB // NoData west edge
		}
		binary.LittleEndian.PutUint32(raw, v)
		b.Write(raw)
	}
	return b.Bytes()
}

// rangeServer serves data with Range support and logs each request's Range.
func rangeServer(t *testing.T, data []byte) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var log []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		log = append(log, r.Header.Get("Range"))
		mu.Unlock()
		http.ServeContent(w, r, "p.IMG", time.Unix(0, 0), bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), log...) }
}

func testConfig(url, out string) config {
	return config{url: url, out: out, registry: filepath.Join(out, "registry.json"), registryVersion: "test", latticeStepMeters: 50,
		client: http.DefaultClient, commandLine: []string{"marsgeo-prep", "-test"}, now: func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }}
}

func TestCropFetchesOnlyNeededRanges(t *testing.T) {
	const n = 1000 // 4,000-byte records: the label fits in record 1
	data := synthetic(n, "PC_REAL")
	srv, requests := rangeServer(t, data)
	out := t.TempDir()
	cfg := testConfig(srv.URL+"/p.IMG", out)
	cfg.crop = "300,250,400,420"
	cfg.sourceSHA256 = strings.Repeat("ab", 32)
	if err := run(cfg); err != nil {
		t.Fatal(err)
	}
	want := []string{"bytes=0-65535", fmt.Sprintf("bytes=%d-%d", 4000+250*4000, 4000+(250+420)*4000-1)}
	if got := requests(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("requests %v, want %v", got, want)
	}
	crop, err := os.ReadFile(filepath.Join(out, "DTEED_PREP_TEST_A01_crop.IMG"))
	if err != nil {
		t.Fatal(err)
	}
	prod, err := marsgeo.OpenProduct(bytes.NewReader(crop), int64(len(crop)))
	if err != nil {
		t.Fatal(err)
	}
	if prod.Label.Samples != 400 || prod.Label.Lines != 420 {
		t.Fatalf("crop is %dx%d", prod.Label.Samples, prod.Label.Lines)
	}
	w, _ := prod.ReadWindow(0, 0, 1, 1)
	if _, v := w.At(0, 0); v != 250*1000+300 {
		t.Fatalf("crop (0,0) = %v, want source (300,250)", v)
	}
	var prov Provenance
	readJSON(t, filepath.Join(out, "DTEED_PREP_TEST_A01_crop.provenance.json"), &prov)
	sum := sha256.Sum256(crop)
	if prov.CropSHA256 != hex.EncodeToString(sum[:]) || prov.CropBytes != len(crop) || prov.CropWindow != [4]int{300, 250, 400, 420} ||
		prov.SourceSHA256 != cfg.sourceSHA256 || prov.SourceBytes != int64(len(data)) || prov.Credit != credit || prov.Generated != "2026-11-15" {
		t.Fatalf("provenance %+v", prov)
	}
	regRaw, _ := os.ReadFile(cfg.registry)
	reg, err := marsgeo.LoadRegistry(regRaw)
	if err != nil {
		t.Fatal(err)
	}
	if e := reg.Entries[0]; e.File != "DTEED_PREP_TEST_A01_crop.IMG" || e.SHA256 != prov.CropSHA256 || e.ValidCentres.Count() == 0 {
		t.Fatalf("entry %+v", e)
	}
	terr, err := reg.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer terr.Close()
	if _, err := terr.Build(5); err != nil {
		t.Fatal(err)
	}
}

func TestFullDownload(t *testing.T) {
	data := synthetic(400, "PC_REAL")
	srv, requests := rangeServer(t, data)
	out := t.TempDir()
	cfg := testConfig(srv.URL+"/p.IMG", out)
	if err := run(cfg); err != nil {
		t.Fatal(err)
	}
	if got := requests(); len(got) != 2 || got[0] != "bytes=0-65535" || got[1] != "" {
		t.Fatalf("requests %v", got)
	}
	got, err := os.ReadFile(filepath.Join(out, "DTEED_PREP_TEST_A01.IMG"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("downloaded file differs: %v", err)
	}
	regRaw, _ := os.ReadFile(cfg.registry)
	reg, err := marsgeo.LoadRegistry(regRaw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	e := reg.Entries[0]
	if e.SHA256 != hex.EncodeToString(sum[:]) || e.ByteSize != int64(len(data)) || e.SourceURL != cfg.url {
		t.Fatalf("entry %+v", e)
	}
	// The lattice equals an in-memory scan of the same bytes.
	prod, _ := marsgeo.OpenProduct(bytes.NewReader(data), int64(len(data)))
	lat, _ := marsgeo.ScanLattice(prod, marsgeo.DefaultGrid, 5, 25)
	if a, b := mustJSON(t, lat), mustJSON(t, e.ValidCentres); a != b {
		t.Fatalf("lattice %s vs %s", b, a)
	}
}

func TestInvalidProductWritesNothing(t *testing.T) {
	for _, crop := range []string{"", "10,10,300,300"} {
		srv, _ := rangeServer(t, synthetic(400, "IEEE_REAL"))
		out := t.TempDir()
		cfg := testConfig(srv.URL+"/p.IMG", out)
		cfg.crop = crop
		if crop != "" {
			cfg.sourceSHA256 = strings.Repeat("0", 64)
		}
		if err := run(cfg); err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Fatalf("crop %q: err = %v", crop, err)
		}
		if entries, _ := os.ReadDir(out); len(entries) != 0 {
			t.Fatalf("crop %q: wrote %v", crop, entries)
		}
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
