package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	return New(Config{
		Addr:            ":0",
		WSPath:          "/ws",
		AllowedOrigins:  []string{"*"},
		MaxMessageBytes: 32 * 1024,
		ShutdownTimeout: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

func TestWebSocketPing(t *testing.T) {
	s := testServer(t)
	httpServer := httptest.NewServer(s.httpServer.Handler)
	defer httpServer.Close()
	defer s.hub.CloseAll()

	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	var welcome Envelope
	if err := conn.ReadJSON(&welcome); err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	if welcome.Type != "welcome" {
		t.Fatalf("expected welcome, got %q", welcome.Type)
	}

	if err := conn.WriteJSON(Envelope{ID: "req-1", Type: "ping"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	var pong Envelope
	if err := conn.ReadJSON(&pong); err != nil {
		t.Fatalf("read pong: %v", err)
	}
	if pong.ID != "req-1" || pong.Type != "pong" {
		t.Fatalf("expected pong for req-1, got id=%q type=%q", pong.ID, pong.Type)
	}
}
