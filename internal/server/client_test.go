package server

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/space-chonkers/server/internal/protocol"
)

func expectError(t *testing.T, conn *websocket.Conn, id string, code protocol.ErrorCode) {
	t.Helper()
	envelope := readEnvelope(t, conn)
	if envelope.Type != protocol.TypeError {
		t.Fatalf("expected error, got %q", envelope.Type)
	}
	if envelope.ID != id {
		t.Fatalf("expected error id %q, got %q", id, envelope.ID)
	}
	if envelope.Error != code {
		t.Fatalf("expected error %q, got %q", code, envelope.Error)
	}
	payload := decodePayload[protocol.ErrorPayload](t, envelope)
	if payload.Message == "" {
		t.Fatalf("expected human-readable error message for %q", code)
	}
}

func expectPong(t *testing.T, conn *websocket.Conn, id string) protocol.PongPayload {
	t.Helper()
	envelope := readEnvelope(t, conn)
	if envelope.Type != protocol.TypePong {
		t.Fatalf("expected pong, got %q", envelope.Type)
	}
	if envelope.ID != id {
		t.Fatalf("expected pong id %q, got %q", id, envelope.ID)
	}
	payload := decodePayload[protocol.PongPayload](t, envelope)
	if payload.ServerTimeMs <= 0 {
		t.Fatalf("expected server time, got %d", payload.ServerTimeMs)
	}
	if payload.Tick != 0 {
		t.Fatalf("expected tick 0 before a match, got %d", payload.Tick)
	}
	return payload
}

func ping(t *testing.T, conn *websocket.Conn, id string) protocol.PongPayload {
	t.Helper()
	writeEnvelope(t, conn, protocol.Envelope{ID: id, Type: protocol.TypePing})
	return expectPong(t, conn, id)
}

func hello(t *testing.T, conn *websocket.Conn, id, nickname string) protocol.HelloResult {
	t.Helper()
	writeEnvelope(t, conn, protocol.Envelope{
		ID:   id,
		Type: protocol.TypePlayerHello,
		Payload: mustRaw(t, protocol.PlayerHelloRequest{
			ProtocolVersion: protocol.ProtocolVersion,
			Nickname:        nickname,
		}),
	})
	envelope := readEnvelope(t, conn)
	if envelope.Type != protocol.TypePlayerHello {
		t.Fatalf("expected player.hello, got %q", envelope.Type)
	}
	if envelope.ID != id {
		t.Fatalf("expected hello id %q, got %q", id, envelope.ID)
	}
	return decodePayload[protocol.HelloResult](t, envelope)
}

func TestWelcomeIsFirstFrame(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)

	// Write hello before reading so a racing read loop cannot reorder the response.
	encoded, err := protocol.EncodeRequest("hello-1", &protocol.PlayerHelloRequest{
		ProtocolVersion: protocol.ProtocolVersion,
		Nickname:        "ana",
	})
	if err != nil {
		t.Fatalf("encode hello: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, encoded); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	welcome := readWelcome(t, conn)
	if welcome.PlayerID == "" || !strings.HasPrefix(welcome.PlayerID, "p_") {
		t.Fatalf("unexpected playerId %q", welcome.PlayerID)
	}
	if welcome.ProtocolVersion != protocol.ProtocolVersion {
		t.Fatalf("expected protocol version %d, got %d", protocol.ProtocolVersion, welcome.ProtocolVersion)
	}
	if welcome.ServerTimeMs <= 0 {
		t.Fatalf("expected server time, got %d", welcome.ServerTimeMs)
	}

	// The hello written before reading the welcome is answered second.
	response := readEnvelope(t, conn)
	if response.Type != protocol.TypePlayerHello || response.ID != "hello-1" {
		t.Fatalf("expected hello response, got id=%q type=%q", response.ID, response.Type)
	}
	result := decodePayload[protocol.HelloResult](t, response)
	if result.PlayerID != welcome.PlayerID || result.Nickname != "ana" {
		t.Fatalf("unexpected hello result %+v", result)
	}
}

func TestPingBeforeHello(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	clientTime := 1234.5
	writeEnvelope(t, conn, protocol.Envelope{
		ID:      "ping-1",
		Type:    protocol.TypePing,
		Payload: mustRaw(t, protocol.PingRequest{ClientTimeMs: &clientTime}),
	})
	pong := expectPong(t, conn, "ping-1")
	if pong.ClientTimeMs == nil || *pong.ClientTimeMs != clientTime {
		t.Fatalf("expected echoed client time %v, got %v", clientTime, pong.ClientTimeMs)
	}
}

func TestHelloThenPing(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	welcome := readWelcome(t, conn)

	result := hello(t, conn, "hello-1", "ana")
	if result.PlayerID != welcome.PlayerID || result.Nickname != "ana" {
		t.Fatalf("unexpected hello result %+v", result)
	}
	ping(t, conn, "ping-after")
}

func TestDuplicateHelloIsRejectedButSocketStaysOpen(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	hello(t, conn, "hello-1", "ana")
	writeEnvelope(t, conn, protocol.Envelope{
		ID:   "hello-2",
		Type: protocol.TypePlayerHello,
		Payload: mustRaw(t, protocol.PlayerHelloRequest{
			ProtocolVersion: protocol.ProtocolVersion,
			Nickname:        "ana",
		}),
	})
	expectError(t, conn, "hello-2", protocol.CodeAlreadyIdentified)
	ping(t, conn, "ping-after")
}

func TestRequestsBeforeHelloAreRejected(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	writeEnvelope(t, conn, protocol.Envelope{
		ID:      "join-1",
		Type:    protocol.TypeMatchJoin,
		Payload: mustRaw(t, protocol.MatchJoinRequest{Create: boolPtr(true)}),
	})
	expectError(t, conn, "join-1", protocol.CodeNotIdentified)
	ping(t, conn, "ping-after")
}

func TestBaseBuildRemainsUnimplemented(t *testing.T) {
	// B4 owns the build action handler.
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)
	hello(t, conn, "hello-1", "ana")

	writeEnvelope(t, conn, protocol.Envelope{
		ID:      "build-1",
		Type:    protocol.TypeBaseBuild,
		Payload: mustRaw(t, protocol.BaseBuildRequest{CellX: intPtr(1), CellY: intPtr(2)}),
	})
	expectError(t, conn, "build-1", protocol.CodeInternal)
	ping(t, conn, "ping-after")
}

func TestMatchInputMovesPlayerThroughServer(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)
	hello(t, conn, "hello-1", "ana")

	writeEnvelope(t, conn, protocol.Envelope{
		ID:      "join-1",
		Type:    protocol.TypeMatchJoin,
		Payload: mustRaw(t, protocol.MatchJoinRequest{Create: boolPtr(true)}),
	})
	joinEnvelope := readEnvelope(t, conn)
	if joinEnvelope.Type != protocol.TypeMatchJoin || joinEnvelope.ID != "join-1" {
		t.Fatalf("unexpected join response: id=%q type=%q", joinEnvelope.ID, joinEnvelope.Type)
	}
	joined := decodePayload[protocol.MatchJoinResult](t, joinEnvelope)
	if joined.MatchID == "" || joined.State.Phase != protocol.PhaseLobby {
		t.Fatalf("unexpected lobby state: %+v", joined)
	}

	writeEnvelope(t, conn, protocol.Envelope{
		Type: protocol.TypeMatchInput,
		Payload: mustRaw(t, protocol.MatchInputRequest{
			Seq: int64Ptr(0),
		}),
	})
	expectError(t, conn, "", protocol.CodeWrongPhase)

	writeEnvelope(t, conn, protocol.Envelope{ID: "start-1", Type: protocol.TypeMatchStart})
	startEnvelope := readEnvelope(t, conn)
	if startEnvelope.Type != protocol.TypeMatchStart || startEnvelope.ID != "start-1" {
		t.Fatalf("unexpected start response: id=%q type=%q", startEnvelope.ID, startEnvelope.Type)
	}
	startedState := decodePayload[protocol.MatchState](t, readEnvelope(t, conn))
	if startedState.Phase != protocol.PhasePlaying || len(startedState.Players) != 1 {
		t.Fatalf("unexpected started state: %+v", startedState)
	}
	if startedState.Grid != (protocol.GridSize{Width: 50, Height: 50}) ||
		startedState.BaseCellCount != 9 || len(startedState.Constructions) != 9 {
		t.Fatalf("unexpected starting world: grid=%+v base=%d constructions=%d",
			startedState.Grid, startedState.BaseCellCount, len(startedState.Constructions))
	}
	initial := startedState.Players[0]

	writeEnvelope(t, conn, protocol.Envelope{
		Type: protocol.TypeMatchInput,
		Payload: mustRaw(t, protocol.MatchInputRequest{
			Seq:  int64Ptr(0),
			Move: &protocol.InputMove{X: float64Ptr(1), Y: float64Ptr(0)},
		}),
	})
	frameEnvelope := readEnvelope(t, conn)
	if frameEnvelope.Type != protocol.TypeMatchFrame {
		t.Fatalf("expected match.frame, got %q", frameEnvelope.Type)
	}
	frame := decodePayload[protocol.MatchFrame](t, frameEnvelope)
	if len(frame.Players) != 1 {
		t.Fatalf("frame player count = %d, want 1", len(frame.Players))
	}
	moved := frame.Players[0]
	if math.Abs(moved.X-initial.X-DefaultConfig().Rules.PlayerSpeed/DefaultConfig().TickRate) > 1e-12 ||
		moved.Y != initial.Y {
		t.Fatalf("input did not move east from %+v to %+v", initial, moved)
	}
	if moved.Facing != protocol.FacingEast || moved.LastInputSeq != 0 {
		t.Fatalf("input state = %+v, want east facing and seq 0", moved)
	}

	writeEnvelope(t, conn, protocol.Envelope{
		Type: protocol.TypeMatchInput,
		Payload: mustRaw(t, protocol.MatchInputRequest{
			Seq:  int64Ptr(0),
			Move: &protocol.InputMove{X: float64Ptr(-1), Y: float64Ptr(0)},
		}),
	})
	ping(t, conn, "ping-after-stale-input")
}

func TestUnknownTypeStaysOpen(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	writeEnvelope(t, conn, protocol.Envelope{ID: "unknown-1", Type: "not.a.message"})
	expectError(t, conn, "unknown-1", protocol.CodeUnsupportedType)
	ping(t, conn, "ping-after")
}

func TestInvalidFramesStayOpen(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":`)); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}
	expectError(t, conn, "", protocol.CodeInvalidJSON)

	writeEnvelope(t, conn, protocol.Envelope{
		ID:      "bad-1",
		Type:    protocol.TypeBaseBuild,
		Payload: mustRaw(t, map[string]any{}),
	})
	expectError(t, conn, "bad-1", protocol.CodeInvalidPayload)
	ping(t, conn, "ping-after")
}

func TestNicknameValidation(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	for i, nickname := range []string{"", strings.Repeat("x", protocol.MaxNicknameLength+1)} {
		id := "hello-bad-" + string(rune('a'+i))
		writeEnvelope(t, conn, protocol.Envelope{
			ID:   id,
			Type: protocol.TypePlayerHello,
			Payload: mustRaw(t, protocol.PlayerHelloRequest{
				ProtocolVersion: protocol.ProtocolVersion,
				Nickname:        nickname,
			}),
		})
		expectError(t, conn, id, protocol.CodeInvalidPayload)
	}

	result := hello(t, conn, "hello-ok", "ana")
	if result.Nickname != "ana" {
		t.Fatalf("expected hello to succeed after invalid attempts, got %+v", result)
	}
}

func TestVersionMismatchClosesPolicyViolation(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	writeEnvelope(t, conn, protocol.Envelope{
		ID:   "hello-1",
		Type: protocol.TypePlayerHello,
		Payload: mustRaw(t, protocol.PlayerHelloRequest{
			ProtocolVersion: protocol.ProtocolVersion + 1,
			Nickname:        "ana",
		}),
	})
	expectError(t, conn, "hello-1", protocol.CodeProtocolVersionMismatch)
	expectClose(t, conn, websocket.ClosePolicyViolation)
}

func TestBinaryFrameClosesUnsupportedData(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02}); err != nil {
		t.Fatalf("write binary frame: %v", err)
	}
	expectClose(t, conn, websocket.CloseUnsupportedData)
}

func TestShutdownClosesGoingAway(t *testing.T) {
	s := testServer(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	s.hub.CloseAll()
	expectClose(t, conn, websocket.CloseGoingAway)
}

func TestOversizeFrameClosesTooBig(t *testing.T) {
	cfg := testConfig()
	cfg.MaxMessageBytes = 64
	s := newTestServer(t, cfg)
	conn := dialWS(t, s)
	readWelcome(t, conn)

	if err := conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 128))); err != nil {
		t.Fatalf("write oversize frame: %v", err)
	}
	expectClose(t, conn, websocket.CloseMessageTooBig)
}

func mustRaw(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	return encoded
}

func boolPtr(value bool) *bool { return &value }

func intPtr(value int) *int { return &value }

func int64Ptr(value int64) *int64 { return &value }

func float64Ptr(value float64) *float64 { return &value }
