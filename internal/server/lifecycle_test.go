package server

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/match"
	"github.com/space-chonkers/server/internal/protocol"
)

func testServerWorld(teamSize int) game.World {
	return game.World{
		Grid:          game.GridSize{Width: 50, Height: 50},
		BaseCellCount: 4,
		TeamSize:      teamSize,
		Constructions: []game.Construction{
			{ID: "tile", Kind: game.ConstructionBaseTile, CellX: 2, CellY: 1},
			{ID: "stash", Kind: game.ConstructionStash, CellX: 1, CellY: 1},
		},
		Investigation: []game.Cell{{CellX: 5, CellY: 5}},
		Spawns: []game.PlayerSpawn{
			{Slot: 0, X: 1.5, Y: 1.5, Facing: game.FacingNorth},
			{Slot: 1, X: 2.5, Y: 1.5, Facing: game.FacingEast},
			{Slot: 2, X: 1.5, Y: 2.5, Facing: game.FacingSouth},
			{Slot: 3, X: 0.5, Y: 1.5, Facing: game.FacingWest},
		},
		Resources: game.Resources{
			Water: game.ResourceState{
				Pool:  game.ResourcePool{Current: 50, Max: 100},
				Stash: game.ResourceStash{Current: 1, Max: 29, GPS: 0.36},
				CPS:   1.32,
			},
		},
	}
}

func testMatchManager() *match.Manager {
	cfg := testConfig()
	return match.NewManager(match.Options{
		MaxMatches:         cfg.MaxMatches,
		MaxPlayersPerMatch: cfg.MaxPlayersPerMatch,
		Grid:               cfg.Rules.Grid,
		GameConfig:         game.DefaultConfig(),
		GenerateWorld: func(_ int64, teamSize int, _ game.Config) (game.World, error) {
			return testServerWorld(teamSize), nil
		},
		NewSeed: func() (int64, error) { return 1, nil },
	})
}

func testServerWithFakeWorld(t *testing.T) *Server {
	t.Helper()
	return newTestServerWithManager(t, testConfig(), testMatchManager())
}

func joinMatch(t *testing.T, conn *websocket.Conn, id string, request protocol.MatchJoinRequest) protocol.MatchJoinResult {
	t.Helper()
	writeEnvelope(t, conn, protocol.Envelope{
		ID:      id,
		Type:    protocol.TypeMatchJoin,
		Payload: mustRaw(t, request),
	})
	envelope := readEnvelope(t, conn)
	if envelope.Type != protocol.TypeMatchJoin || envelope.ID != id {
		t.Fatalf("expected match.join response %q, got id=%q type=%q", id, envelope.ID, envelope.Type)
	}
	return decodePayload[protocol.MatchJoinResult](t, envelope)
}

func quickJoin(t *testing.T, conn *websocket.Conn, id string) protocol.MatchJoinResult {
	t.Helper()
	return joinMatch(t, conn, id, protocol.MatchJoinRequest{})
}

func createMatch(t *testing.T, conn *websocket.Conn, id string) protocol.MatchJoinResult {
	t.Helper()
	return joinMatch(t, conn, id, protocol.MatchJoinRequest{Create: boolPtr(true)})
}

func joinMatchByID(t *testing.T, conn *websocket.Conn, id, matchID string) protocol.MatchJoinResult {
	t.Helper()
	return joinMatch(t, conn, id, protocol.MatchJoinRequest{MatchID: &matchID})
}

func expectState(t *testing.T, conn *websocket.Conn) protocol.MatchState {
	t.Helper()
	envelope := readEnvelope(t, conn)
	if envelope.Type != protocol.TypeMatchState {
		t.Fatalf("expected match.state, got %q", envelope.Type)
	}
	return decodePayload[protocol.MatchState](t, envelope)
}

func expectStartStates(t *testing.T, conn *websocket.Conn) (protocol.MatchState, protocol.MatchState) {
	t.Helper()
	countdown := expectState(t, conn)
	playing := expectState(t, conn)
	if countdown.Phase != protocol.PhaseCountdown || playing.Phase != protocol.PhasePlaying {
		t.Fatalf("start states = %q, %q", countdown.Phase, playing.Phase)
	}
	return countdown, playing
}

func startMatch(t *testing.T, conn *websocket.Conn, id string) (protocol.MatchState, protocol.MatchState) {
	t.Helper()
	writeEnvelope(t, conn, protocol.Envelope{ID: id, Type: protocol.TypeMatchStart})
	envelope := readEnvelope(t, conn)
	if envelope.Type != protocol.TypeMatchStart || envelope.ID != id {
		t.Fatalf("expected match.start response %q, got id=%q type=%q", id, envelope.ID, envelope.Type)
	}
	_ = decodePayload[protocol.OKPayload](t, envelope)
	return expectStartStates(t, conn)
}

func TestMatchJoinCreateJoinAndLeave(t *testing.T) {
	s := testServerWithFakeWorld(t)
	host := dialWS(t, s)
	readWelcome(t, host)
	hostHello := hello(t, host, "hello-host", "ana")

	created := createMatch(t, host, "create-1")
	if created.MatchID == "" || created.State.Phase != protocol.PhaseLobby {
		t.Fatalf("unexpected create result %+v", created)
	}
	if created.State.HostID != hostHello.PlayerID || created.State.TeamSize != 1 {
		t.Fatalf("unexpected lobby state %+v", created.State)
	}
	if len(created.State.Players) != 1 || created.State.Players[0].Name != "ana" {
		t.Fatalf("unexpected lobby players %+v", created.State.Players)
	}

	guest := dialWS(t, s)
	readWelcome(t, guest)
	guestHello := hello(t, guest, "hello-guest", "bo")
	joined := joinMatchByID(t, guest, "join-1", created.MatchID)
	if joined.State.TeamSize != 2 || len(joined.State.Players) != 2 {
		t.Fatalf("unexpected joined state %+v", joined.State)
	}
	if joined.State.HostID != hostHello.PlayerID {
		t.Fatalf("host = %q", joined.State.HostID)
	}

	hosted := expectState(t, host)
	if hosted.TeamSize != 2 || hosted.Players[1].ID != guestHello.PlayerID {
		t.Fatalf("host did not see the join: %+v", hosted)
	}

	writeEnvelope(t, guest, protocol.Envelope{ID: "leave-1", Type: protocol.TypeMatchLeave})
	leaveResponse := readEnvelope(t, guest)
	if leaveResponse.Type != protocol.TypeMatchLeave || leaveResponse.ID != "leave-1" {
		t.Fatalf("unexpected leave response %+v", leaveResponse)
	}
	_ = decodePayload[protocol.OKPayload](t, leaveResponse)

	afterLeave := expectState(t, host)
	if afterLeave.TeamSize != 1 || len(afterLeave.Players) != 1 || afterLeave.HostID != hostHello.PlayerID {
		t.Fatalf("state after leave = %+v", afterLeave)
	}

	writeEnvelope(t, guest, protocol.Envelope{ID: "leave-2", Type: protocol.TypeMatchLeave})
	expectError(t, guest, "leave-2", protocol.CodeNotInMatch)
}

func TestMatchJoinFull(t *testing.T) {
	s := testServerWithFakeWorld(t)
	match, err := s.matches.Create("p_host", "ana", discardSink{})
	if err != nil {
		t.Fatalf("create match: %v", err)
	}
	for i := 1; i < 4; i++ {
		playerID := fmt.Sprintf("p_%d", i)
		if _, err := s.matches.Join(match.ID(), playerID, playerID, discardSink{}); err != nil {
			t.Fatalf("join %s: %v", playerID, err)
		}
	}

	conn := dialWS(t, s)
	readWelcome(t, conn)
	hello(t, conn, "hello-1", "fifth")
	matchID := match.ID()
	writeEnvelope(t, conn, protocol.Envelope{
		ID:      "join-1",
		Type:    protocol.TypeMatchJoin,
		Payload: mustRaw(t, protocol.MatchJoinRequest{MatchID: &matchID}),
	})
	expectError(t, conn, "join-1", protocol.CodeMatchFull)
}

func TestMatchStartEmitsFullState(t *testing.T) {
	s := testServerWithFakeWorld(t)
	host := dialWS(t, s)
	readWelcome(t, host)
	hostHello := hello(t, host, "hello-host", "ana")
	created := createMatch(t, host, "create-1")

	guest := dialWS(t, s)
	readWelcome(t, guest)
	hello(t, guest, "hello-guest", "bo")
	joinMatchByID(t, guest, "join-1", created.MatchID)
	_ = expectState(t, host) // lobby update from the join

	countdown, playing := startMatch(t, host, "start-1")
	guestCountdown, guestPlaying := expectStartStates(t, guest)

	if countdown.PhaseEndsAtMs != countdown.ServerTimeMs {
		t.Fatalf("countdown must end immediately: ends=%d now=%d", countdown.PhaseEndsAtMs, countdown.ServerTimeMs)
	}
	if guestCountdown.Phase != protocol.PhaseCountdown || guestPlaying.Phase != protocol.PhasePlaying {
		t.Fatalf("guest phases = %q, %q", guestCountdown.Phase, guestPlaying.Phase)
	}
	if playing.Tick != 0 || guestPlaying.Tick != 0 {
		t.Fatalf("start tick = %d/%d", playing.Tick, guestPlaying.Tick)
	}
	if playing.MatchID != created.MatchID || playing.Schema != protocol.StateSchema {
		t.Fatalf("unexpected identity %+v", playing)
	}
	if playing.HostID != hostHello.PlayerID || playing.TeamSize != 2 || playing.BaseCellCount != 4 {
		t.Fatalf("unexpected start state %+v", playing)
	}
	if playing.Grid != (protocol.GridSize{Width: 50, Height: 50}) {
		t.Fatalf("grid = %+v", playing.Grid)
	}
	if len(playing.Players) != 2 || len(playing.Constructions) != 2 || len(playing.Investigation) != 1 {
		t.Fatalf("start collections players=%d constructions=%d investigation=%d",
			len(playing.Players), len(playing.Constructions), len(playing.Investigation))
	}
	wantConstructions := []protocol.Construction{
		{ID: "stash", Kind: protocol.ConstructionStash, CellX: 1, CellY: 1},
		{ID: "tile", Kind: protocol.ConstructionBaseTile, CellX: 2, CellY: 1},
	}
	for i, want := range wantConstructions {
		if playing.Constructions[i] != want {
			t.Fatalf("construction %d = %+v, want %+v", i, playing.Constructions[i], want)
		}
	}
	if playing.Investigation[0] != (protocol.InvestigationCell{CellX: 5, CellY: 5}) {
		t.Fatalf("investigation = %+v", playing.Investigation)
	}
	if playing.Resources.Water.Pool.Current <= 0 || playing.Resources.Water.Pool.Max <= 0 {
		t.Fatalf("water not initialized: %+v", playing.Resources.Water)
	}
	if playing.Phenomenon.Value.PhenomenonType() != protocol.PhenomenonNoneType {
		t.Fatalf("start phenomenon = %v", playing.Phenomenon.Value)
	}

	writeEnvelope(t, guest, protocol.Envelope{ID: "start-guest", Type: protocol.TypeMatchStart})
	expectError(t, guest, "start-guest", protocol.CodeNotHost)
	writeEnvelope(t, host, protocol.Envelope{ID: "start-again", Type: protocol.TypeMatchStart})
	expectError(t, host, "start-again", protocol.CodeWrongPhase)

	third := dialWS(t, s)
	readWelcome(t, third)
	hello(t, third, "hello-third", "cy")
	writeEnvelope(t, third, protocol.Envelope{
		ID:      "join-third",
		Type:    protocol.TypeMatchJoin,
		Payload: mustRaw(t, protocol.MatchJoinRequest{MatchID: &created.MatchID}),
	})
	expectError(t, third, "join-third", protocol.CodeMatchInProgress)
}

func TestMatchQuickJoin(t *testing.T) {
	s := testServerWithFakeWorld(t)
	first := dialWS(t, s)
	readWelcome(t, first)
	hello(t, first, "hello-first", "ana")
	created := quickJoin(t, first, "quick-1")

	second := dialWS(t, s)
	readWelcome(t, second)
	hello(t, second, "hello-second", "bo")
	joined := quickJoin(t, second, "quick-2")
	if joined.MatchID != created.MatchID {
		t.Fatalf("quickjoin created a second lobby: %q != %q", joined.MatchID, created.MatchID)
	}
	_ = expectState(t, first)

	startMatch(t, first, "start-1")
	_, _ = expectStartStates(t, second)

	third := dialWS(t, s)
	readWelcome(t, third)
	hello(t, third, "hello-third", "cy")
	fresh := quickJoin(t, third, "quick-3")
	if fresh.MatchID == created.MatchID {
		t.Fatal("quickjoin joined a started match")
	}
	if fresh.State.Phase != protocol.PhaseLobby || fresh.State.TeamSize != 1 {
		t.Fatalf("unexpected quickjoin state %+v", fresh.State)
	}
}

func TestMatchJoinAndStartErrors(t *testing.T) {
	s := testServerWithFakeWorld(t)
	conn := dialWS(t, s)
	readWelcome(t, conn)
	hello(t, conn, "hello-1", "ana")

	writeEnvelope(t, conn, protocol.Envelope{ID: "leave-1", Type: protocol.TypeMatchLeave})
	expectError(t, conn, "leave-1", protocol.CodeNotInMatch)
	writeEnvelope(t, conn, protocol.Envelope{ID: "start-1", Type: protocol.TypeMatchStart})
	expectError(t, conn, "start-1", protocol.CodeNotInMatch)

	missing := "NOPE"
	writeEnvelope(t, conn, protocol.Envelope{
		ID:      "join-missing",
		Type:    protocol.TypeMatchJoin,
		Payload: mustRaw(t, protocol.MatchJoinRequest{MatchID: &missing}),
	})
	expectError(t, conn, "join-missing", protocol.CodeMatchNotFound)

	created := createMatch(t, conn, "create-1")
	writeEnvelope(t, conn, protocol.Envelope{
		ID:      "join-again",
		Type:    protocol.TypeMatchJoin,
		Payload: mustRaw(t, protocol.MatchJoinRequest{MatchID: &created.MatchID}),
	})
	expectError(t, conn, "join-again", protocol.CodeAlreadyInMatch)
}

func TestDisconnectMigratesHost(t *testing.T) {
	s := testServerWithFakeWorld(t)
	host := dialWS(t, s)
	readWelcome(t, host)
	hello(t, host, "hello-host", "ana")
	created := createMatch(t, host, "create-1")

	guest := dialWS(t, s)
	readWelcome(t, guest)
	guestHello := hello(t, guest, "hello-guest", "bo")
	joinMatchByID(t, guest, "join-1", created.MatchID)
	_ = expectState(t, host)

	if err := host.Close(); err != nil {
		t.Fatalf("close host: %v", err)
	}

	migrated := expectState(t, guest)
	if migrated.HostID != guestHello.PlayerID {
		t.Fatalf("host = %q, want %q", migrated.HostID, guestHello.PlayerID)
	}
	if migrated.TeamSize != 1 || len(migrated.Players) != 1 || migrated.Players[0].ID != guestHello.PlayerID {
		t.Fatalf("state after host disconnect = %+v", migrated)
	}
	if migrated.Phase != protocol.PhaseLobby {
		t.Fatalf("phase = %q", migrated.Phase)
	}

	startMatch(t, guest, "start-1")
}
