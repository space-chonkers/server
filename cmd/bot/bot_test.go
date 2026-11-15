package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/space-chonkers/server/internal/protocol"
)

func TestBotFakeServerActions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*protocol.MatchState)
		want   protocol.MessageType
		check  func(*testing.T, protocol.Envelope)
	}{
		{
			name: "build",
			want: protocol.TypeBaseBuild,
			check: func(t *testing.T, envelope protocol.Envelope) {
				var request protocol.BaseBuildRequest
				if err := json.Unmarshal(envelope.Payload, &request); err != nil {
					t.Fatalf("decode build: %v", err)
				}
				if request.CellX == nil || request.CellY == nil ||
					*request.CellX != 3 || *request.CellY != 1 {
					t.Fatalf("build target = %+v, want (3,1)", request)
				}
			},
		},
		{
			name: "collect",
			mutate: func(state *protocol.MatchState) {
				state.Resources.Water.Pool.Current = 1
			},
			want: protocol.TypeStashCollect,
		},
		{
			name: "fix",
			mutate: func(state *protocol.MatchState) {
				state.Phenomenon = protocol.ActivePhenomenon{Value: protocol.PhenomenonSolar{
					StartedAtMs:           time.Now().UnixMilli(),
					FixCostEnergy:         20,
					GpsReductionPerSecond: 0.10,
				}}
			},
			want: protocol.TypePhenomenonFix,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := testState()
			if test.mutate != nil {
				test.mutate(&state)
			}

			server, messages := fakeBotServer(t, state)
			defer server.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- run(ctx, botConfig{URL: wsURL(server), Players: 1})
			}()

			action, sawInput := waitForAction(t, messages, test.want, 3*time.Second)
			if !sawInput {
				t.Fatal("bot did not send match.input before its action")
			}
			if test.check != nil {
				test.check(t, action)
			}

			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("run: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("bot did not stop after cancellation")
			}
		})
	}
}

func TestBotStopsAtDuration(t *testing.T) {
	server, _ := fakeBotServer(t, testState())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, botConfig{URL: wsURL(server), Players: 1})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bot did not stop at its duration")
	}
}

func TestChooseBuildAndPath(t *testing.T) {
	b := newBot(1, "", "bot-1")
	b.grid = protocol.GridSize{Width: 10, Height: 10}
	b.base = map[cell]struct{}{
		{1, 1}: {}, {2, 1}: {}, {2, 2}: {},
	}
	b.goals = []cell{{5, 1}}
	build := b.chooseBuild()

	if build == nil {
		t.Fatal("no build candidate")
	}
	if build.target != (cell{3, 1}) || build.stand != (cell{2, 1}) ||
		build.dir != protocol.FacingEast {
		t.Fatalf("build choice = target %+v stand %+v dir %q",
			build.target, build.stand, build.dir)
	}

	path := b.path(cell{1, 1}, cell{2, 2})
	if len(path) != 3 || path[0] != (cell{1, 1}) || path[2] != (cell{2, 2}) {
		t.Fatalf("path = %+v", path)
	}
}

func testState() protocol.MatchState {
	return protocol.MatchState{
		MatchID:       "AB12",
		Schema:        protocol.StateSchema,
		ServerTimeMs:  time.Now().UnixMilli(),
		Phase:         protocol.PhasePlaying,
		HostID:        "p_bot",
		Grid:          protocol.GridSize{Width: 10, Height: 10},
		BaseCellCount: 2,
		TeamSize:      1,
		Players: []protocol.PlayerState{{
			ID: "p_bot", Slot: 0, Name: "bot-1", Facing: protocol.FacingEast,
			X: 2.5, Y: 1.5, LastInputSeq: -1,
		}},
		Constructions: []protocol.Construction{
			{ID: "tile", Kind: protocol.ConstructionBaseTile, CellX: 1, CellY: 1},
			{ID: "stash", Kind: protocol.ConstructionStash, CellX: 2, CellY: 1},
		},
		Investigation: []protocol.InvestigationCell{{CellX: 5, CellY: 1}},
		Resources:     testResources(50),
		Phenomenon:    protocol.ActivePhenomenon{Value: protocol.PhenomenonNone{}},
	}
}

func testResources(pool float64) protocol.Resources {
	resource := protocol.ResourceState{
		Pool:  protocol.ResourcePool{Current: pool, Max: 100},
		Stash: protocol.ResourceStash{Current: 0, Max: 10, GPS: 0},
		CPS:   0,
	}
	return protocol.Resources{
		Water: resource, Oxygen: resource, Energy: resource, Food: resource,
	}
}

func fakeBotServer(t *testing.T, state protocol.MatchState) (*httptest.Server, <-chan protocol.Envelope) {
	t.Helper()
	messages := make(chan protocol.Envelope, 256)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

		send := func(envelope protocol.Envelope) bool {
			return conn.WriteJSON(envelope) == nil
		}
		read := func() (protocol.Envelope, bool) {
			var envelope protocol.Envelope
			if err := conn.ReadJSON(&envelope); err != nil {
				return envelope, false
			}
			return envelope, true
		}

		welcome, err := protocol.NewEvent(protocol.TypeWelcome, protocol.Welcome{
			PlayerID:        "p_bot",
			ProtocolVersion: protocol.ProtocolVersion,
			ServerTimeMs:    time.Now().UnixMilli(),
			TickRate:        30,
			FrameRate:       15,
			Limits: protocol.Limits{
				MaxMessageBytes: 32768, MaxPlayersPerMatch: 4,
				MaxNicknameLength: protocol.MaxNicknameLength, MaxMatches: 64,
			},
			Rules: protocol.Rules{
				Grid: state.Grid, InvestigationCount: 1,
				BuildCost:   protocol.BuildCost{Water: 5, Oxygen: 5, Energy: 5},
				PlayerSpeed: 4, PhenomenonRollSeconds: 30,
			},
		})
		if err != nil || !send(welcome) {
			return
		}

		envelope, ok := read()
		if !ok || envelope.Type != protocol.TypePlayerHello || envelope.ID != "hello-1" {
			return
		}
		hello, _ := protocol.NewResponse("hello-1", protocol.TypePlayerHello,
			protocol.HelloResult{PlayerID: "p_bot", Nickname: "bot-1"})
		if !send(hello) {
			return
		}

		envelope, ok = read()
		if !ok || envelope.Type != protocol.TypeMatchJoin || envelope.ID != "join-1" {
			return
		}
		lobby := state
		lobby.Phase = protocol.PhaseLobby
		join, _ := protocol.NewResponse("join-1", protocol.TypeMatchJoin,
			protocol.MatchJoinResult{MatchID: state.MatchID, State: lobby})
		if !send(join) {
			return
		}

		envelope, ok = read()
		if !ok || envelope.Type != protocol.TypeMatchStart || envelope.ID != "start-1" {
			return
		}
		countdown := state
		countdown.Phase = protocol.PhaseCountdown
		playing := state
		playing.Phase = protocol.PhasePlaying
		countdownEvent, _ := protocol.NewEvent(protocol.TypeMatchState, countdown)
		playingEvent, _ := protocol.NewEvent(protocol.TypeMatchState, playing)
		start, _ := protocol.NewResponse("start-1", protocol.TypeMatchStart, protocol.OKPayload{})
		if !send(countdownEvent) || !send(playingEvent) || !send(start) {
			return
		}

		_ = conn.SetReadDeadline(time.Time{})
		for {
			envelope, ok := read()
			if !ok {
				return
			}
			select {
			case messages <- envelope:
			default:
			}
		}
	}))

	return server, messages
}

func waitForAction(
	t *testing.T,
	messages <-chan protocol.Envelope,
	want protocol.MessageType,
	timeout time.Duration,
) (protocol.Envelope, bool) {
	t.Helper()
	deadline := time.After(timeout)
	sawInput := false
	for {
		select {
		case envelope := <-messages:
			if envelope.Type == protocol.TypeMatchInput {
				sawInput = true
				if envelope.ID != "" {
					t.Fatalf("match.input carried id %q", envelope.ID)
				}
				var request protocol.MatchInputRequest
				if err := json.Unmarshal(envelope.Payload, &request); err != nil {
					t.Fatalf("decode input: %v", err)
				}
				if request.Seq == nil || *request.Seq < 1 {
					t.Fatalf("input seq = %v", request.Seq)
				}
			}
			if envelope.Type == want {
				return envelope, sawInput
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

func wsURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
}
