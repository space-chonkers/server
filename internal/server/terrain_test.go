package server

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestMarsTerrainRoute(t *testing.T) {
	s := testServer(t)
	for path, want := range map[string]int{
		"/v1/mars-terrain/1": http.StatusOK,
		"/v1/mars-terrain/x": http.StatusBadRequest,
	} {
		rec := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s: status %d, want %d", path, rec.Code, want)
		}
	}

	// The default ALLOWED_ORIGINS is "*", so a browser client on any origin can read the map.
	rec := getTerrain(s, "/v1/mars-terrain/1", "http://localhost:5173")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); rec.Code != http.StatusOK || got != "http://localhost:5173" {
		t.Fatalf("browser GET: status %d, Access-Control-Allow-Origin %q", rec.Code, got)
	}
}

func TestMarsTerrainCORS(t *testing.T) {
	cfg := testConfig()
	cfg.AllowedOrigins = []string{"https://play.example"}
	s := newTestServer(t, cfg)
	for _, tc := range []struct {
		name, path, origin, wantAllow string
		wantStatus                    int
	}{
		{"allowed origin", "/v1/mars-terrain/7", "https://play.example", "https://play.example", http.StatusOK},
		{"disallowed origin", "/v1/mars-terrain/7", "https://evil.example", "", http.StatusOK},
		{"no origin", "/v1/mars-terrain/7", "", "", http.StatusOK},
		{"allowed origin, bad seed", "/v1/mars-terrain/x", "https://play.example", "https://play.example", http.StatusBadRequest},
	} {
		rec := getTerrain(s, tc.path, tc.origin)
		h := rec.Header()
		if rec.Code != tc.wantStatus || h.Get("Access-Control-Allow-Origin") != tc.wantAllow {
			t.Errorf("%s: status %d, Access-Control-Allow-Origin %q; want %d, %q",
				tc.name, rec.Code, h.Get("Access-Control-Allow-Origin"), tc.wantStatus, tc.wantAllow)
		}
		if !slices.Contains(h.Values("Vary"), "Origin") || h.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("%s: Vary %q, Access-Control-Allow-Credentials %q", tc.name, h.Values("Vary"), h.Get("Access-Control-Allow-Credentials"))
		}
	}

	cfg.AllowedOrigins = []string{"https://a.example", "*"}
	if got := getTerrain(newTestServer(t, cfg), "/v1/mars-terrain/7", "https://any.example").Header().Get("Access-Control-Allow-Origin"); got != "https://any.example" {
		t.Fatalf("wildcard in list: Access-Control-Allow-Origin %q", got)
	}
}

func getTerrain(s *Server, path, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}
