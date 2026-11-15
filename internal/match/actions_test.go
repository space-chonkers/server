package match

import (
	"errors"
	"testing"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

func actionTestManager(t *testing.T, first, second *fakeSink) (*Manager, *Match) {
	t.Helper()
	config := game.DefaultConfig()
	manager := NewManager(Options{
		MaxMatches:         2,
		MaxPlayersPerMatch: 2,
		Grid:               protocol.GridSize{Width: 50, Height: 50},
		GameConfig:         config,
		NewSeed: func() (int64, error) {
			return 1, nil
		},
	})
	match, err := manager.Create("create", "p1", "one", first, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join("join", match.ID(), "p2", "two", second, 1000); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start("p1", 1000); err != nil {
		t.Fatal(err)
	}
	return manager, match
}

func setActionPlayerPosition(t *testing.T, match *Match, playerID string, x, y float64, facing game.Facing) {
	t.Helper()
	match.mu.Lock()
	defer match.mu.Unlock()
	if err := match.initializeMovementPlayersLocked(); err != nil {
		t.Fatal(err)
	}
	for slot, member := range match.members {
		if member == nil || member.PlayerID != playerID {
			continue
		}
		match.players[slot].X = x
		match.players[slot].Y = y
		match.players[slot].Facing = protocol.Facing(facing)
		return
	}
	t.Fatalf("player %s not found", playerID)
}

func frontBuildPosition(t *testing.T, match *Match) (float64, float64, game.Facing, game.Cell) {
	t.Helper()
	match.mu.RLock()
	defer match.mu.RUnlock()

	minX, minY := match.world.Grid.Width, match.world.Grid.Height
	maxX, maxY := -1, -1
	for _, construction := range match.world.Constructions {
		minX = min(minX, construction.CellX)
		minY = min(minY, construction.CellY)
		maxX = max(maxX, construction.CellX)
		maxY = max(maxY, construction.CellY)
	}
	switch {
	case maxX+1 < match.world.Grid.Width:
		return float64(maxX) + 0.5, float64(minY) + 0.5, game.FacingEast,
			game.Cell{CellX: maxX + 1, CellY: minY}
	case minX > 0:
		return float64(minX) + 0.5, float64(minY) + 0.5, game.FacingWest,
			game.Cell{CellX: minX - 1, CellY: minY}
	case maxY+1 < match.world.Grid.Height:
		return float64(minX) + 0.5, float64(maxY) + 0.5, game.FacingSouth,
			game.Cell{CellX: minX, CellY: maxY + 1}
	default:
		return float64(minX) + 0.5, float64(minY) + 0.5, game.FacingNorth,
			game.Cell{CellX: minX, CellY: minY - 1}
	}
}

func sinkMessages(sink *fakeSink) []protocol.Envelope {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]protocol.Envelope(nil), sink.envelopes...)
}

func TestBuildPublishesToOthersAndWinOverridesDepletion(t *testing.T) {
	builderSink, otherSink := &fakeSink{}, &fakeSink{}
	manager, match := actionTestManager(t, builderSink, otherSink)

	playerX, playerY, facing, target := frontBuildPosition(t, match)
	match.mu.Lock()
	match.world.Investigation = []game.Cell{target}
	match.world.Resources.Water.Pool.Current = 5
	match.world.Resources.Oxygen.Pool.Current = 5
	match.world.Resources.Energy.Pool.Current = 5
	match.mu.Unlock()
	setActionPlayerPosition(t, match, "p1", playerX, playerY, facing)

	result, ended, err := manager.Build("p1", target, 2500)
	if err != nil {
		t.Fatal(err)
	}
	if result.Construction.CellX != target.CellX || result.Construction.CellY != target.CellY {
		t.Fatalf("build result = %+v, want target %+v", result, target)
	}
	if ended == nil || ended.Outcome != protocol.OutcomeWin ||
		ended.Reason != protocol.ReasonAllInvestigated {
		t.Fatalf("ended result = %+v, want win despite depleted pools", ended)
	}
	if ended.Stats.ElapsedMs != 1500 || ended.Stats.BaseCells != 10 ||
		ended.Stats.InvestigationCovered != 1 || ended.Stats.InvestigationTotal != 1 {
		t.Fatalf("win stats = %+v", ended.Stats)
	}
	if _, _, err := manager.Build("p1", target, 3000); protocol.CodeOf(err) != protocol.CodeMatchOver {
		t.Fatal("terminal match accepted another build")
	}

	builderMessages := sinkMessages(builderSink)
	if got := builderMessages[len(builderMessages)-1].Type; got != protocol.TypeMatchEnded {
		t.Fatalf("builder final message = %q, want match.ended", got)
	}
	otherMessages := sinkMessages(otherSink)
	if len(otherMessages) < 2 ||
		otherMessages[len(otherMessages)-2].Type != protocol.TypeMatchBuild ||
		otherMessages[len(otherMessages)-1].Type != protocol.TypeMatchEnded {
		t.Fatalf("other member messages = %+v, want build then ended", otherMessages)
	}
}

func TestBuildDepletionLosesWhenGoalsRemain(t *testing.T) {
	builderSink, otherSink := &fakeSink{}, &fakeSink{}
	manager, match := actionTestManager(t, builderSink, otherSink)

	playerX, playerY, facing, target := frontBuildPosition(t, match)
	match.mu.Lock()
	match.world.Resources.Water.Pool.Current = 5
	match.world.Resources.Oxygen.Pool.Current = 5
	match.world.Resources.Energy.Pool.Current = 5
	match.mu.Unlock()
	setActionPlayerPosition(t, match, "p1", playerX, playerY, facing)

	_, ended, err := manager.Build("p1", target, 2500)
	if err != nil {
		t.Fatal(err)
	}
	if ended == nil || ended.Outcome != protocol.OutcomeLoss ||
		ended.Reason != protocol.ReasonResourceDepleted ||
		ended.Stats.DepletedResource != protocol.ResourceWater {
		t.Fatalf("ended result = %+v, want water depletion loss", ended)
	}
}

func TestCollectTransfersStashAndCanEndDepletedMatch(t *testing.T) {
	first, second := &fakeSink{}, &fakeSink{}
	manager, match := actionTestManager(t, first, second)

	match.mu.Lock()
	stash := game.Cell{}
	for _, construction := range match.world.Constructions {
		if construction.Kind == game.ConstructionStash {
			stash = game.Cell{CellX: construction.CellX, CellY: construction.CellY}
			break
		}
	}
	match.world.Resources.Water.Pool.Current = 0
	match.world.Resources.Water.Stash.Current = 2
	match.world.Resources.Water.Pool.Max = 1
	match.world.Resources.Oxygen.Pool.Current = 10
	match.world.Resources.Oxygen.Stash.Current = 3
	match.world.Resources.Oxygen.Pool.Max = 20
	match.mu.Unlock()
	setActionPlayerPosition(t, match, "p1", float64(stash.CellX)+0.5, float64(stash.CellY)+0.5, game.FacingNorth)

	result, ended, err := manager.Collect("p1", 2500)
	if err != nil {
		t.Fatal(err)
	}
	if result.Collected.Water != 1 || result.Lost.Water != 1 ||
		result.Collected.Oxygen != 3 || result.Lost.Oxygen != 0 {
		t.Fatalf("collect result = %+v", result)
	}
	if ended != nil {
		t.Fatalf("collect recovered depleted water pool but ended match: %+v", ended)
	}

	match.mu.Lock()
	match.world.Resources.Water.Pool.Current = 0
	match.mu.Unlock()
	result, ended, err = manager.Collect("p1", 3000)
	if err != nil {
		t.Fatal(err)
	}
	if ended == nil || ended.Outcome != protocol.OutcomeLoss ||
		ended.Stats.DepletedResource != protocol.ResourceWater {
		t.Fatalf("collect with a depleted pool ended = %+v", ended)
	}
	if _, _, err := manager.Collect("p1", 3500); protocol.CodeOf(err) != protocol.CodeMatchOver {
		t.Fatal("terminal match accepted a subsequent action")
	}
}

func TestCollectAndBuildReturnContractErrors(t *testing.T) {
	first, second := &fakeSink{}, &fakeSink{}
	manager, match := actionTestManager(t, first, second)
	_, _, err := manager.Collect("missing", 2000)
	if protocol.CodeOf(err) != protocol.CodeNotInMatch {
		t.Fatalf("collect unknown player error = %v", err)
	}

	_, _, err = manager.Build("p1", game.Cell{CellX: -1}, 2000)
	if protocol.CodeOf(err) != protocol.CodeOutOfBounds {
		t.Fatalf("build out of bounds error = %v", err)
	}

	// A failed action must leave the published match snapshot untouched.
	match.mu.RLock()
	baseCount := match.world.BaseCellCount
	match.mu.RUnlock()
	if baseCount != 9 {
		t.Fatalf("failed action changed base cell count to %d", baseCount)
	}

	_, _, err = manager.Collect("p1", 2000)
	if protocol.CodeOf(err) != protocol.CodeNotAtStash {
		t.Fatalf("collect away from stash error = %v", err)
	}
}

func TestBuildInsufficientResourcesIsAtomicAndReportsMissing(t *testing.T) {
	first, second := &fakeSink{}, &fakeSink{}
	manager, match := actionTestManager(t, first, second)
	playerX, playerY, facing, target := frontBuildPosition(t, match)
	setActionPlayerPosition(t, match, "p1", playerX, playerY, facing)

	match.mu.Lock()
	match.world.Resources.Water.Pool.Current = 1
	match.world.Resources.Oxygen.Pool.Current = 2
	match.world.Resources.Energy.Pool.Current = 10
	before := match.world
	match.mu.Unlock()

	_, _, err := manager.Build("p1", target, 2000)
	if protocol.CodeOf(err) != protocol.CodeInsufficientResources {
		t.Fatalf("Build error = %v, want insufficient_resources", err)
	}
	var insufficient *InsufficientResourcesError
	if !errors.As(err, &insufficient) ||
		len(insufficient.Missing) != 2 ||
		insufficient.Missing[0] != protocol.ResourceWater ||
		insufficient.Missing[1] != protocol.ResourceOxygen {
		t.Fatalf("missing resources error = %#v", err)
	}
	match.mu.RLock()
	defer match.mu.RUnlock()
	if match.world.BaseCellCount != before.BaseCellCount ||
		match.world.Resources.Water.Pool.Current != before.Resources.Water.Pool.Current ||
		match.world.Resources.Oxygen.Pool.Current != before.Resources.Oxygen.Pool.Current ||
		match.world.Resources.Energy.Pool.Current != before.Resources.Energy.Pool.Current {
		t.Fatal("failed build partially modified match state")
	}
}
