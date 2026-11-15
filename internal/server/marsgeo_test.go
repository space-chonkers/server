package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/space-chonkers/server/internal/marsgeo"
	"github.com/space-chonkers/server/internal/protocol"
)

// Phase 4A.5 smoke tests: the Mars terrain route is mounted, it never blocks
// startup, it closes with the server, and the existing routes are unchanged.

func TestMarsTerrainRouteWithoutData(t *testing.T) {
	s := testServer(t) // no MarsGeoDataDir
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mars-terrain/42", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Content-Type") != "application/problem+json" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d, headers %v", rec.Code, rec.Header())
	}
	var p struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil || p.Code != "data-unavailable" || !p.Retryable {
		t.Fatalf("problem %+v, %v", p, err)
	}
	rec = httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mars-terrain/01", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad seed status %d", rec.Code)
	}
}

func TestExistingRoutesUnchanged(t *testing.T) {
	s := testServer(t)
	for path, keys := range map[string][]string{
		"/healthz": {"status", "clients"},
		"/readyz":  {"status"},
		"/":        {"service", "version", "websocket", "clients"},
	} {
		rec := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]any
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || len(body) != len(keys) {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		for _, k := range keys {
			if _, ok := body[k]; !ok {
				t.Fatalf("%s: missing %q in %s", path, k, rec.Body)
			}
		}
	}
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path: %d", rec.Code)
	}
	conn := dialWS(t, s)
	if env := readEnvelope(t, conn); env.Type != protocol.TypeWelcome {
		t.Fatalf("websocket first message %q, want welcome", env.Type)
	}
}

func TestShutdownClosesMarsTerrain(t *testing.T) {
	s := testServer(t)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.terrain.Build(1); !errors.Is(err, marsgeo.ErrDataUnavailable) {
		t.Fatalf("Build after Shutdown: %v", err)
	}
}

func TestConfigReadsMarsGeoDataDir(t *testing.T) {
	t.Setenv("MARSGEO_DATA_DIR", " /data/hirise ")
	if got := ConfigFromEnv().MarsGeoDataDir; got != "/data/hirise" {
		t.Fatalf("MarsGeoDataDir = %q", got)
	}
	t.Setenv("MARSGEO_DATA_DIR", "")
	if got := ConfigFromEnv().MarsGeoDataDir; got != "" {
		t.Fatalf("unset MarsGeoDataDir = %q", got)
	}
}

// TestMarsTerrainRouteWithFullProduct runs when MARSGEO_DATA_DIR holds the
// production A02 product: the real server serves a payload over HTTP.
func TestMarsTerrainRouteWithFullProduct(t *testing.T) {
	dir := os.Getenv("MARSGEO_DATA_DIR")
	if dir == "" {
		t.Skip("MARSGEO_DATA_DIR not set")
	}
	cfg := testConfig()
	cfg.MarsGeoDataDir = dir
	s := newTestServer(t, cfg)
	defer s.Shutdown(context.Background())
	srv := httptest.NewServer(s.httpServer.Handler)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/mars-terrain/2026")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var d marsgeo.MarsTerrainDataV1
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&d) != nil {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if d.Seed != 2026 || len(d.Elevation.SamplesMeters) != 2601 || d.Elevation.Datum != marsgeo.Datum || *d.Source.ProductID != "DTEED_029716_1980_029426_1980_A02" {
		t.Fatalf("payload %+v", d.Source)
	}
}
