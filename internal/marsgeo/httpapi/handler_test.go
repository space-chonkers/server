package httpapi

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/space-chonkers/server/internal/marsgeo"
)

// fakeBuilder returns a fixed payload for any seed, or err.
type fakeBuilder struct {
	err   error
	calls []uint32
}

func (f *fakeBuilder) Build(seed uint32) (marsgeo.MarsTerrainDataV1, error) {
	f.calls = append(f.calls, seed)
	if f.err != nil {
		return marsgeo.MarsTerrainDataV1{}, f.err
	}
	id := "DTEED_TEST_A01"
	samples := make([]float64, 9)
	for i := range samples {
		samples[i] = 1000.25 + float64(i)
	}
	return marsgeo.MarsTerrainDataV1{
		Version:   1,
		Center:    marsgeo.Coordinate{Latitude: 17.8, LongitudeEast: 232.49},
		Grid:      marsgeo.Grid{Columns: 2, Rows: 2, CellSizeMeters: 10},
		Elevation: marsgeo.ElevationSamples{Sampling: "cell-corners", Columns: 3, Rows: 3, SamplesMeters: samples, Datum: marsgeo.Datum},
		Source:    marsgeo.Source{DatasetID: marsgeo.HiRISEDTMDatasetID, Provider: marsgeo.HiRISEProvider, ResolutionMeters: 2, ProductID: &id},
		Seed:      seed,
	}, nil
}

func newMux(b Builder, version string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(Route, NewHandler(b, version, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{RetryAfter: 30 * time.Second}))
	return mux
}

func do(t *testing.T, h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func expectedBody(t *testing.T, seed uint32) []byte {
	d, _ := (&fakeBuilder{}).Build(seed)
	b, err := json.Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestExactPayloadBytes(t *testing.T) {
	rec := do(t, newMux(&fakeBuilder{}, "v1"), "GET", "/v1/mars-terrain/1234", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, expectedBody(t, 1234)) {
		t.Fatalf("body differs from json.Marshal of the payload:\n%s", got)
	}
	h := rec.Header()
	checks := map[string]string{
		"Content-Type":     "application/json",
		"Cache-Control":    "public, max-age=86400",
		"Vary":             "Accept-Encoding",
		"Content-Encoding": "",
		"Content-Length":   fmt.Sprint(rec.Body.Len()),
	}
	for k, want := range checks {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if etag := h.Get("ETag"); !strings.HasPrefix(etag, `"`) || strings.HasPrefix(etag, "W/") || len(etag) != 66 {
		t.Errorf("ETag %q is not a strong 64-hex tag", etag)
	}
}

func TestGzipNegotiation(t *testing.T) {
	mux := newMux(&fakeBuilder{}, "v1")
	want := expectedBody(t, 7)
	for ae, gz := range map[string]bool{
		"":                      false,
		"gzip":                  true,
		"GZIP":                  true,
		"x-gzip":                true,
		"gzip, deflate, br":     true,
		"gzip;q=0":              false,
		"gzip; q=0.000":         false,
		"gzip;q=0.001":          true,
		"*":                     true,
		"*;q=0":                 false,
		"gzip;q=0, *":           false,
		"br":                    false,
		"identity":              false,
		"gzip;q=banana":         false,
		"deflate;q=1, *;q=0.5":  true,
		"br;q=1.0, gzip;q=0.8 ": true,
	} {
		rec := do(t, mux, "GET", "/v1/mars-terrain/7", map[string]string{"Accept-Encoding": ae})
		if rec.Code != 200 || rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("%q: status %d, Vary %q", ae, rec.Code, rec.Header().Get("Vary"))
		}
		body := rec.Body.Bytes()
		if gotGz := rec.Header().Get("Content-Encoding") == "gzip"; gotGz != gz {
			t.Errorf("Accept-Encoding %q: gzip = %v, want %v", ae, gotGz, gz)
			continue
		}
		if gz {
			zr, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			body, _ = io.ReadAll(zr)
			if !strings.HasSuffix(rec.Header().Get("ETag"), `-gzip"`) {
				t.Errorf("%q: gzip ETag %q lacks -gzip", ae, rec.Header().Get("ETag"))
			}
		}
		if !bytes.Equal(body, want) {
			t.Errorf("%q: decoded body differs", ae)
		}
	}
}

func TestETagAndConditionalRequests(t *testing.T) {
	mux := newMux(&fakeBuilder{}, "v1")
	first := do(t, mux, "GET", "/v1/mars-terrain/5", nil)
	etag := first.Header().Get("ETag")
	if again := do(t, mux, "GET", "/v1/mars-terrain/5", nil).Header().Get("ETag"); again != etag {
		t.Fatal("ETag not deterministic")
	}
	gzTag := do(t, mux, "GET", "/v1/mars-terrain/5", map[string]string{"Accept-Encoding": "gzip"}).Header().Get("ETag")
	if gzTag == etag {
		t.Fatal("gzip and identity representations share an ETag")
	}
	if other := do(t, newMux(&fakeBuilder{}, "v2"), "GET", "/v1/mars-terrain/5", nil).Header().Get("ETag"); other == etag {
		t.Fatal("registry version not part of the ETag")
	}
	if other := do(t, mux, "GET", "/v1/mars-terrain/6", nil).Header().Get("ETag"); other == etag {
		t.Fatal("different seeds share an ETag")
	}

	for name, c := range map[string]struct {
		inm, ae string
		want    int
	}{
		"exact":                  {etag, "", 304},
		"list":                   {`"abc", ` + etag + `, "def"`, "", 304},
		"star":                   {"*", "", 304},
		"weak form (RFC 9110)":   {"W/" + etag, "", 304},
		"gzip tag, gzip request": {gzTag, "gzip", 304},
		"mismatch":               {`"abc"`, "", 200},
		"identity tag, gzip req": {etag, "gzip", 200},
		"gzip tag, identity req": {gzTag, "", 200},
	} {
		rec := do(t, mux, "GET", "/v1/mars-terrain/5", map[string]string{"If-None-Match": c.inm, "Accept-Encoding": c.ae})
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", name, rec.Code, c.want)
			continue
		}
		if c.want == 304 {
			h := rec.Header()
			if rec.Body.Len() != 0 || h.Get("ETag") == "" || h.Get("Vary") != "Accept-Encoding" || h.Get("Cache-Control") != "public, max-age=86400" {
				t.Errorf("%s: 304 body %d bytes, headers %v", name, rec.Body.Len(), h)
			}
		}
	}
}

func TestHEAD(t *testing.T) {
	srv := httptest.NewServer(newMux(&fakeBuilder{}, "v1"))
	defer srv.Close()
	// No transparent gzip, so GET and HEAD negotiate the same representation.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	get, err := client.Get(srv.URL + "/v1/mars-terrain/9")
	if err != nil {
		t.Fatal(err)
	}
	get.Body.Close()
	resp, err := client.Head(srv.URL + "/v1/mars-terrain/9")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(body) != 0 || resp.Header.Get("ETag") != get.Header.Get("ETag") || resp.ContentLength != get.ContentLength {
		t.Fatalf("HEAD: %d, %d body bytes, ETag %q vs %q, length %d vs %d", resp.StatusCode, len(body), resp.Header.Get("ETag"), get.Header.Get("ETag"), resp.ContentLength, get.ContentLength)
	}
	post, err := client.Post(srv.URL+"/v1/mars-terrain/9", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d, want 405", post.StatusCode)
	}
}

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func checkProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string, retryable bool) problem {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "application/problem+json" || h.Get("Cache-Control") != "no-store" || h.Get("ETag") != "" {
		t.Fatalf("problem headers %v", h)
	}
	var p problem
	dec := json.NewDecoder(rec.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Type != "urn:space-chonkers:problem:"+code || p.Code != code || p.Status != status || p.Retryable != retryable || p.Title == "" || p.Detail == "" {
		t.Fatalf("problem %+v", p)
	}
	return p
}

func TestProblemResponses(t *testing.T) {
	secret := "/srv/data/DTEED_X.IMG: checksum deadbeef"
	cases := []struct {
		err       error
		status    int
		code      string
		retryable bool
	}{
		{fmt.Errorf("%w: footprint leaves raster", marsgeo.ErrNoCoverage), 422, CodeNoCoverage, false},
		{fmt.Errorf("%w: %s", marsgeo.ErrDataUnavailable, secret), 503, CodeDataUnavailable, true},
		{fmt.Errorf("%w: centre", marsgeo.ErrInvalidCoordinate), 500, CodeInternal, true},
		{errors.New(secret), 500, CodeInternal, true},
	}
	for _, c := range cases {
		rec := do(t, newMux(&fakeBuilder{err: c.err}, "v1"), "GET", "/v1/mars-terrain/1", map[string]string{"Accept-Encoding": "gzip"})
		p := checkProblem(t, rec, c.status, c.code, c.retryable)
		if strings.Contains(rec.Body.String(), "deadbeef") || strings.Contains(p.Detail, "/srv") {
			t.Errorf("%s: internal error text leaked: %+v", c.code, p)
		}
		if ra := rec.Header().Get("Retry-After"); (c.status == 503) != (ra == "30") {
			t.Errorf("%s: Retry-After %q", c.code, ra)
		}
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s: problem body was compressed", c.code)
		}
	}
}

func TestSeedValidation(t *testing.T) {
	for _, s := range []string{"-1", "+1", "01", "00", "4294967296", "99999999999", "12345678901", "abc", "1%20", "%201", "1e3", "0x10", "1.0", "%D9%A1", "18446744073709551616"} {
		b := &fakeBuilder{}
		rec := do(t, newMux(b, "v1"), "GET", "/v1/mars-terrain/"+s, nil)
		checkProblem(t, rec, 400, CodeInvalidRequest, false)
		if len(b.calls) != 0 {
			t.Errorf("seed %q reached the builder", s)
		}
	}
	for s, want := range map[string]uint32{"0": 0, "7": 7, "4294967295": 4294967295, "1000000000": 1000000000} {
		b := &fakeBuilder{}
		rec := do(t, newMux(b, "v1"), "GET", "/v1/mars-terrain/"+s, nil)
		if rec.Code != 200 || len(b.calls) != 1 || b.calls[0] != want {
			t.Errorf("seed %q: status %d, calls %v", s, rec.Code, b.calls)
		}
	}
}

// TestRealCropPayload serves the committed real HiRISE crop through the
// engine's lifecycle wrapper and checks the body is exactly the engine's
// payload bytes.
func TestRealCropPayload(t *testing.T) {
	dir := filepath.Join("..", "testdata", "hirise")
	raw, err := os.ReadFile(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := marsgeo.LoadRegistry(raw)
	if err != nil {
		t.Fatal(err)
	}
	lazy := marsgeo.NewLazyTerrain(reg, dir, 0)
	defer lazy.Close()
	mux := newMux(lazy, lazy.RegistryVersion())

	terr, err := reg.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := terr.Build(2026)
	terr.Close()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, _ := json.Marshal(&want)

	rec := do(t, mux, "GET", "/v1/mars-terrain/2026", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("status %d, encoding %q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	gzSize := rec.Body.Len()
	zr, _ := gzip.NewReader(rec.Body)
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, wantBytes) {
		t.Fatal("served payload differs from Terrain.Build bytes")
	}
	t.Logf("payload %d bytes, gzip %d bytes", len(wantBytes), gzSize)
	var d marsgeo.MarsTerrainDataV1
	if err := json.Unmarshal(got, &d); err != nil || d.Seed != 2026 || d.Elevation.Datum != "mars-2000-areoid" || len(d.Elevation.SamplesMeters) != 2601 || *d.Source.ProductID != "DTEED_029716_1980_029426_1980_A02" {
		t.Fatalf("decoded payload %+v, %v", d.Source, err)
	}
	// The golden payload for seed 2026, pinned in Phase 4A, is what the endpoint serves.
	golden, err := os.ReadFile(filepath.Join(dir, "golden_seed_2026.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(golden), got) {
		t.Log("served bytes differ from golden_seed_2026.json (expected only off amd64 v1); values are checked in marsgeo")
	}
}

func TestDataUnavailableWithoutDataDir(t *testing.T) {
	reg, err := marsgeo.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	lazy := marsgeo.NewLazyTerrain(reg, "", 0)
	defer lazy.Close()
	rec := do(t, newMux(lazy, lazy.RegistryVersion()), "GET", "/v1/mars-terrain/1", nil)
	checkProblem(t, rec, 503, CodeDataUnavailable, true)
}
