package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/space-chonkers/server/internal/marsgeo"
	"github.com/space-chonkers/server/internal/marsgeo/httpapi"
	"github.com/space-chonkers/server/internal/match"
)

// Version is set at build time with -ldflags "-X ...".
var Version = "dev"

type Server struct {
	cfg        Config
	log        *slog.Logger
	hub        *Hub
	matches    *match.Manager
	terrain    *marsgeo.LazyTerrain
	httpServer *http.Server
	upgrader   websocket.Upgrader
}

func New(cfg Config, log *slog.Logger) *Server {
	server := &Server{
		cfg: cfg,
		log: log,
		hub: NewHub(),
		matches: match.NewManager(match.Options{
			MaxMatches:         cfg.MaxMatches,
			MaxPlayersPerMatch: cfg.MaxPlayersPerMatch,
		}),
	}
	server.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     server.checkOrigin,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", server.handleRoot)
	mux.HandleFunc("GET /healthz", server.handleHealth)
	mux.HandleFunc("GET /readyz", server.handleReady)
	mux.HandleFunc("GET "+cfg.WSPath, server.handleWebSocket)
	mux.Handle(httpapi.Route, server.marsTerrainHandler())

	server.httpServer = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server
}

func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.hub.CloseAll()
	s.matches.Close()
	err := s.httpServer.Shutdown(ctx)
	// After Shutdown, so in-flight terrain requests finish first. Close also
	// waits for any Build still running if Shutdown timed out.
	if cerr := s.terrain.Close(); cerr != nil {
		s.log.Warn("close mars terrain", "error", cerr)
	}
	return err
}

// marsTerrainHandler wires the standalone marsgeo engine to HTTP. The
// registry is embedded; its product files are opened lazily from
// MarsGeoDataDir on the first request, so startup never waits on or fails
// for terrain data.
func (s *Server) marsTerrainHandler() http.Handler {
	reg, err := marsgeo.DefaultRegistry()
	if err != nil {
		s.log.Error("mars terrain registry", "error", err) // build defect; the endpoint answers 503
	}
	s.terrain = marsgeo.NewLazyTerrain(reg, s.cfg.MarsGeoDataDir, marsgeo.DefaultOpenRetryInterval)
	return httpapi.NewHandler(s.terrain, s.terrain.RegistryVersion(), s.log, httpapi.Options{})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":   "jam-server",
		"version":   Version,
		"websocket": s.cfg.WSPath,
		"clients":   s.hub.Count(),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"clients": s.hub.Count(),
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade failed", "error", err)
		return
	}

	client, err := NewClient(s.hub, conn, s.cfg, s.log)
	if err != nil {
		s.log.Error("create client", "error", err)
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "internal error"), time.Now().Add(writeWait))
		_ = conn.Close()
		return
	}
	s.hub.Add(client)
	client.Start()
	s.log.Info("client connected", "client", client.ID(), "remote", r.RemoteAddr, "clients", s.hub.Count())
}

func (s *Server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	s.log.Warn("rejected websocket origin", "origin", origin)
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
