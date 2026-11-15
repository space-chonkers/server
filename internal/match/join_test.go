package match

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

func testWorld(teamSize int) game.World {
	return game.World{
		Grid:          game.GridSize{Width: 50, Height: 50},
		BaseCellCount: 9,
		TeamSize:      teamSize,
		Constructions: []game.Construction{
			{ID: "base-5-6", Kind: game.ConstructionBaseTile, CellX: 5, CellY: 6},
			{ID: "stash", Kind: game.ConstructionStash, CellX: 5, CellY: 5},
			{ID: "base-4-5", Kind: game.ConstructionBaseTile, CellX: 4, CellY: 5},
		},
		Investigation: []game.Cell{
			{CellX: 10, CellY: 2},
			{CellX: 3, CellY: 9},
			{CellX: 3, CellY: 1},
		},
		Spawns: []game.PlayerSpawn{
			{Slot: 0, X: 5.5, Y: 4.5, Facing: game.FacingNorth},
			{Slot: 1, X: 6.5, Y: 5.5, Facing: game.FacingEast},
			{Slot: 2, X: 5.5, Y: 6.5, Facing: game.FacingSouth},
			{Slot: 3, X: 4.5, Y: 5.5, Facing: game.FacingWest},
		},
		Resources: game.Resources{
			Water: game.ResourceState{
				Pool:  game.ResourcePool{Current: 50, Max: 100},
				Stash: game.ResourceStash{Current: 1, Max: 29, GPS: 0.36},
				CPS:   1.32,
			},
			Oxygen: game.ResourceState{
				Pool:  game.ResourcePool{Current: 60, Max: 120},
				Stash: game.ResourceStash{Current: 2, Max: 30, GPS: 0.4},
				CPS:   1.5,
			},
			Energy: game.ResourceState{
				Pool:  game.ResourcePool{Current: 70, Max: 140},
				Stash: game.ResourceStash{Current: 3, Max: 31, GPS: 0.2},
				CPS:   1.1,
			},
			Food: game.ResourceState{
				Pool:  game.ResourcePool{Current: 40, Max: 80},
				Stash: game.ResourceStash{Current: 4, Max: 32, GPS: 0.15},
				CPS:   0.6,
			},
		},
	}
}

func testOptions() Options {
	return Options{
		MaxMatches:         16,
		MaxPlayersPerMatch: 4,
		Grid:               protocol.GridSize{Width: 50, Height: 50},
		GameConfig:         game.DefaultConfig(),
		GenerateWorld: func(_ int64, teamSize int, _ game.Config) (game.World, error) {
			return testWorld(teamSize), nil
		},
		NewSeed: func() (int64, error) { return 42, nil },
	}
}

func newTestManager(t *testing.T, generate WorldGenerator) *Manager {
	t.Helper()
	opts := testOptions()
	if generate != nil {
		opts.GenerateWorld = generate
	}
	return NewManager(opts)
}

func mustStart(t *testing.T, manager *Manager, matchID, playerID string) *Started {
	t.Helper()
	started, err := manager.Start(matchID, playerID)
	if err != nil {
		t.Fatalf("start %s by %s: %v", matchID, playerID, err)
	}
	return started
}

func TestLobbyStateTracksMembersAndHost(t *testing.T) {
	manager := newTestManager(t, nil)
	created := mustCreate(t, manager, "p_1", &fakeSink{})
	mustJoin(t, manager, created.ID(), "p_2", &fakeSink{})

	state := created.State(1234)
	if state.MatchID != created.ID() || state.Schema != protocol.StateSchema {
		t.Fatalf("unexpected identity %+v", state)
	}
	if state.Phase != protocol.PhaseLobby || state.Tick != 0 || state.ServerTimeMs != 1234 {
		t.Fatalf("unexpected lifecycle %+v", state)
	}
	if state.HostID != "p_1" || state.TeamSize != 2 {
		t.Fatalf("unexpected membership host=%q teamSize=%d", state.HostID, state.TeamSize)
	}
	if state.Grid != (protocol.GridSize{Width: 50, Height: 50}) || state.BaseCellCount != 0 {
		t.Fatalf("unexpected map %+v", state)
	}
	if len(state.Constructions) != 0 || len(state.Investigation) != 0 {
		t.Fatal("lobby must not publish a map")
	}
	if state.Resources != (protocol.Resources{}) {
		t.Fatalf("lobby resources must be zero: %+v", state.Resources)
	}
	if state.Phenomenon.Value.PhenomenonType() != protocol.PhenomenonNoneType {
		t.Fatalf("lobby phenomenon = %v", state.Phenomenon.Value)
	}
	if len(state.Players) != 2 {
		t.Fatalf("players = %d, want 2", len(state.Players))
	}
	for i, player := range state.Players {
		if player.Slot != i || player.Facing != protocol.FacingNorth ||
			player.X != 0 || player.Y != 0 || player.LastInputSeq != -1 {
			t.Fatalf("unexpected lobby player %d: %+v", i, player)
		}
	}

	if err := manager.Leave(created.ID(), "p_1"); err != nil {
		t.Fatalf("leave host: %v", err)
	}
	state = created.State(1235)
	if state.HostID != "p_2" || state.TeamSize != 1 || len(state.Players) != 1 || state.Players[0].ID != "p_2" {
		t.Fatalf("host migration not reflected in state: %+v", state)
	}
}

func TestStartReturnsCountdownThenPlayingState(t *testing.T) {
	var gotSeed int64
	var gotTeamSize int
	var gotConfig game.Config
	manager := newTestManager(t, func(seed int64, teamSize int, config game.Config) (game.World, error) {
		gotSeed, gotTeamSize, gotConfig = seed, teamSize, config
		return testWorld(teamSize), nil
	})
	created := mustCreate(t, manager, "p_1", &fakeSink{})
	mustJoin(t, manager, created.ID(), "p_2", &fakeSink{})

	started := mustStart(t, manager, created.ID(), "p_1")
	if gotSeed != 42 || gotTeamSize != 2 || gotConfig.InvestigationCount != game.DefaultConfig().InvestigationCount {
		t.Fatalf("generator inputs seed=%d teamSize=%d config=%+v", gotSeed, gotTeamSize, gotConfig)
	}

	countdown := started.Countdown
	if countdown.Phase != protocol.PhaseCountdown {
		t.Fatalf("countdown phase = %q", countdown.Phase)
	}
	if countdown.PhaseEndsAtMs != countdown.ServerTimeMs {
		t.Fatalf("M1 countdown must end immediately: ends=%d now=%d", countdown.PhaseEndsAtMs, countdown.ServerTimeMs)
	}
	if started.Playing.Phase != protocol.PhasePlaying || started.Playing.PhaseEndsAtMs != 0 {
		t.Fatalf("playing lifecycle = %+v", started.Playing)
	}

	state := started.Playing
	if state.MatchID != created.ID() || state.Schema != protocol.StateSchema || state.Tick != 0 {
		t.Fatalf("unexpected identity %+v", state)
	}
	if state.HostID != "p_1" || state.TeamSize != 2 || state.BaseCellCount != 9 {
		t.Fatalf("unexpected membership %+v", state)
	}
	if state.Grid != (protocol.GridSize{Width: 50, Height: 50}) {
		t.Fatalf("grid = %+v", state.Grid)
	}
	if len(state.Players) != 2 || state.Players[0].ID != "p_1" || state.Players[1].ID != "p_2" {
		t.Fatalf("players = %+v", state.Players)
	}
	if state.Players[0].Facing != protocol.FacingNorth || state.Players[0].X != 5.5 || state.Players[0].Y != 4.5 {
		t.Fatalf("spawn 0 = %+v", state.Players[0])
	}
	if state.Players[1].Facing != protocol.FacingEast || state.Players[1].X != 6.5 || state.Players[1].Y != 5.5 {
		t.Fatalf("spawn 1 = %+v", state.Players[1])
	}
	if state.Players[0].LastInputSeq != -1 || state.Players[1].LastInputSeq != -1 {
		t.Fatalf("expected unacknowledged inputs, got %+v", state.Players)
	}

	wantConstructions := []protocol.Construction{
		{ID: "base-4-5", Kind: protocol.ConstructionBaseTile, CellX: 4, CellY: 5},
		{ID: "stash", Kind: protocol.ConstructionStash, CellX: 5, CellY: 5},
		{ID: "base-5-6", Kind: protocol.ConstructionBaseTile, CellX: 5, CellY: 6},
	}
	if len(state.Constructions) != len(wantConstructions) {
		t.Fatalf("constructions = %+v", state.Constructions)
	}
	for i, want := range wantConstructions {
		if state.Constructions[i] != want {
			t.Fatalf("construction %d = %+v, want %+v", i, state.Constructions[i], want)
		}
	}

	wantInvestigation := []protocol.InvestigationCell{
		{CellX: 3, CellY: 1},
		{CellX: 3, CellY: 9},
		{CellX: 10, CellY: 2},
	}
	if len(state.Investigation) != len(wantInvestigation) {
		t.Fatalf("investigation = %+v", state.Investigation)
	}
	for i, want := range wantInvestigation {
		if state.Investigation[i] != want {
			t.Fatalf("investigation %d = %+v, want %+v", i, state.Investigation[i], want)
		}
	}

	if state.Resources.Water.Pool.Current != 50 || state.Resources.Water.Pool.Max != 100 ||
		state.Resources.Water.Stash.Current != 1 || state.Resources.Water.Stash.Max != 29 ||
		state.Resources.Water.Stash.GPS != 0.36 || state.Resources.Water.CPS != 1.32 {
		t.Fatalf("water = %+v", state.Resources.Water)
	}
	if state.Resources.Food.CPS != 0.6 || state.Resources.Food.Stash.GPS != 0.15 {
		t.Fatalf("food = %+v", state.Resources.Food)
	}
	if state.Phenomenon.Value.PhenomenonType() != protocol.PhenomenonNoneType {
		t.Fatalf("phenomenon = %v", state.Phenomenon.Value)
	}
}

func TestStartRequiresHostAndLobby(t *testing.T) {
	manager := newTestManager(t, nil)
	created := mustCreate(t, manager, "p_1", &fakeSink{})
	mustJoin(t, manager, created.ID(), "p_2", &fakeSink{})

	if _, err := manager.Start("NOPE", "p_1"); protocol.CodeOf(err) != protocol.CodeMatchNotFound {
		t.Fatalf("unknown match: %v", err)
	}
	if _, err := manager.Start(created.ID(), "p_3"); protocol.CodeOf(err) != protocol.CodeNotInMatch {
		t.Fatalf("outsider: %v", err)
	}
	if _, err := manager.Start(created.ID(), "p_2"); protocol.CodeOf(err) != protocol.CodeNotHost {
		t.Fatalf("non-host: %v", err)
	}
	mustStart(t, manager, created.ID(), "p_1")
	if _, err := manager.Start(created.ID(), "p_1"); protocol.CodeOf(err) != protocol.CodeWrongPhase {
		t.Fatalf("second start: %v", err)
	}
}

func TestStartFailureLeavesLobby(t *testing.T) {
	var calls atomic.Int32
	manager := newTestManager(t, func(_ int64, teamSize int, _ game.Config) (game.World, error) {
		if calls.Add(1) == 1 {
			return game.World{}, fmt.Errorf("boom")
		}
		return testWorld(teamSize), nil
	})
	created := mustCreate(t, manager, "p_1", &fakeSink{})

	if _, err := manager.Start(created.ID(), "p_1"); protocol.CodeOf(err) != protocol.CodeInternal {
		t.Fatalf("expected internal, got %v", err)
	}
	if state := created.State(1); state.Phase != protocol.PhaseLobby {
		t.Fatalf("state after failure = %+v", state)
	}
	mustStart(t, manager, created.ID(), "p_1")
	if calls.Load() != 2 {
		t.Fatalf("generator calls = %d, want 2", calls.Load())
	}
}

func TestJoinStartedMatchIsRejected(t *testing.T) {
	manager := newTestManager(t, nil)
	created := mustCreate(t, manager, "p_1", &fakeSink{})
	mustStart(t, manager, created.ID(), "p_1")

	if _, err := manager.Join(created.ID(), "p_2", "p_2", &fakeSink{}); protocol.CodeOf(err) != protocol.CodeMatchInProgress {
		t.Fatalf("join started match: %v", err)
	}
}

func TestQuickJoinSkipsStartedMatches(t *testing.T) {
	manager := newTestManager(t, nil)
	started := mustCreate(t, manager, "p_1", &fakeSink{})
	mustStart(t, manager, started.ID(), "p_1")

	joined, err := manager.JoinAny("p_2", "p_2", &fakeSink{})
	if err != nil {
		t.Fatalf("join any: %v", err)
	}
	if joined == started {
		t.Fatal("quickjoin joined a started match")
	}
	if joined.State(1).Phase != protocol.PhaseLobby || joined.HostID() != "p_2" {
		t.Fatalf("unexpected quickjoin match %+v", joined.Members())
	}
}

func TestQuickJoinIsAtomic(t *testing.T) {
	opts := testOptions()
	opts.MaxPlayersPerMatch = 2
	manager := NewManager(opts)

	const players = 10
	var wait sync.WaitGroup
	for i := 0; i < players; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			playerID := fmt.Sprintf("p_%d", i)
			if _, err := manager.JoinAny(playerID, playerID, &fakeSink{}); err != nil {
				t.Errorf("join any %s: %v", playerID, err)
			}
		}(i)
	}
	wait.Wait()

	if manager.Count() != players/2 {
		t.Fatalf("matches = %d, want %d", manager.Count(), players/2)
	}
	total := 0
	seen := make(map[string]bool, players)
	for id, match := range manager.matches {
		if len(match.Members()) > 2 {
			t.Fatalf("match %s has %d members", id, len(match.Members()))
		}
		for _, member := range match.Members() {
			if seen[member.PlayerID] {
				t.Fatalf("player %s joined twice", member.PlayerID)
			}
			seen[member.PlayerID] = true
			total++
		}
	}
	if total != players {
		t.Fatalf("members = %d, want %d", total, players)
	}
}

func TestLeaveWhilePlayingKeepsFrozenTeamSize(t *testing.T) {
	manager := newTestManager(t, nil)
	created := mustCreate(t, manager, "p_1", &fakeSink{})
	mustJoin(t, manager, created.ID(), "p_2", &fakeSink{})
	mustStart(t, manager, created.ID(), "p_1")

	if err := manager.Leave(created.ID(), "p_2"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	state := created.State(1)
	if state.Phase != protocol.PhasePlaying || state.TeamSize != 2 || len(state.Players) != 1 {
		t.Fatalf("state after playing leave = %+v", state)
	}
	if state.Players[0].ID != "p_1" || state.HostID != "p_1" {
		t.Fatalf("unexpected remaining player %+v", state.Players)
	}
}

func TestConcurrentStartOnlyOneWins(t *testing.T) {
	var generations atomic.Int32
	manager := newTestManager(t, func(_ int64, teamSize int, _ game.Config) (game.World, error) {
		generations.Add(1)
		return testWorld(teamSize), nil
	})
	created := mustCreate(t, manager, "p_1", &fakeSink{})

	var wait sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := manager.Start(created.ID(), "p_1")
			results <- err
		}()
	}
	wait.Wait()
	close(results)

	successes, wrongPhase := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case protocol.CodeOf(err) == protocol.CodeWrongPhase:
			wrongPhase++
		default:
			t.Fatalf("unexpected start error: %v", err)
		}
	}
	if successes != 1 || wrongPhase != 1 {
		t.Fatalf("successes=%d wrongPhase=%d", successes, wrongPhase)
	}
	if generations.Load() != 1 {
		t.Fatalf("generations = %d, want 1", generations.Load())
	}
}
