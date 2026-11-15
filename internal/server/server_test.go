package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/space-chonkers/server/internal/match"
	"github.com/space-chonkers/server/internal/protocol"
)

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Addr = ":0"
	cfg.ShutdownTimeout = time.Second
	return cfg
}

func testServer(t *testing.T) *Server {
	t.Helper()
	return newTestServer(t, testConfig())
}

func newTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	s := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(s.stopFrameLoop)
	return s
}

func newTestServerWithManager(t *testing.T, cfg Config, matches *match.Manager) *Server {
	t.Helper()
	s := newWithManager(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), matches)
	t.Cleanup(s.stopFrameLoop)
	return s
}

func dialWS(t *testing.T, s *Server) *websocket.Conn {
	t.Helper()

	httpServer := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(httpServer.Close)
	t.Cleanup(s.hub.CloseAll)

	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + s.cfg.WSPath
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readEnvelope(t *testing.T, conn *websocket.Conn) protocol.Envelope {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var envelope protocol.Envelope
	if err := conn.ReadJSON(&envelope); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	return envelope
}

func writeEnvelope(t *testing.T, conn *websocket.Conn, envelope protocol.Envelope) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set write deadline: %v", err)
	}
	if err := conn.WriteJSON(envelope); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
}

func decodePayload[T any](t *testing.T, envelope protocol.Envelope) T {
	t.Helper()
	var payload T
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("decode %s payload: %v", envelope.Type, err)
	}
	return payload
}

// readEnvelopeOfType skips lossy match frames while waiting for a reliable
// message. Frames may be interleaved with any response once a match is playing.
func readEnvelopeOfType(t *testing.T, conn *websocket.Conn, typ protocol.MessageType) protocol.Envelope {
	t.Helper()
	for {
		envelope := readEnvelope(t, conn)
		if envelope.Type == typ {
			return envelope
		}
		if envelope.Type != protocol.TypeMatchFrame {
			t.Fatalf("expected %q (skipping frames), got %q", typ, envelope.Type)
		}
	}
}

func readWelcome(t *testing.T, conn *websocket.Conn) protocol.Welcome {
	t.Helper()
	envelope := readEnvelope(t, conn)
	if envelope.Type != protocol.TypeWelcome {
		t.Fatalf("expected welcome first, got %q", envelope.Type)
	}
	return decodePayload[protocol.Welcome](t, envelope)
}

func expectClose(t *testing.T, conn *websocket.Conn, code int) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, _, err := conn.ReadMessage()
	if !websocket.IsCloseError(err, code) {
		t.Fatalf("expected close %d, got %v", code, err)
	}
}

func TestHealth(t *testing.T) {
	s := testServer(t)
	recorder := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
}

type discardSink struct{}

func (discardSink) Send(protocol.Envelope) {}

func TestShutdownClosesMatches(t *testing.T) {
	s := testServer(t)
	if _, err := s.matches.Create("create", "p_1", "ana", discardSink{}, 0); err != nil {
		t.Fatalf("create match: %v", err)
	}
	if s.matches.Count() != 1 {
		t.Fatalf("expected one match, got %d", s.matches.Count())
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if s.matches.Count() != 0 {
		t.Fatalf("expected no matches after shutdown, got %d", s.matches.Count())
	}
}
