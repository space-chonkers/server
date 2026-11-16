package server

import (
	"testing"
	"time"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/match"
	"github.com/space-chonkers/server/internal/protocol"
)

// winWorld places a single investigation cell in front of slot 0 (north of
// the stash) so one paid build is enough to cover it and end the match.
func winWorld() game.World {
	constructions := []game.Construction{}
	for x := 10; x <= 12; x++ {
		for y := 10; y <= 12; y++ {
			kind := game.ConstructionBaseTile
			if x == 10 && y == 10 {
				kind = game.ConstructionStash
			}
			constructions = append(constructions, game.Construction{ID: "c", Kind: kind, CellX: x, CellY: y})
		}
	}
	return game.World{
		Grid:          game.GridSize{Width: 50, Height: 50},
		BaseCellCount: 9,
		TeamSize:      1,
		Constructions: constructions,
		Investigation: []game.Cell{{CellX: 10, CellY: 9}},
		Spawns: []game.PlayerSpawn{
			{Slot: 0, X: 10.5, Y: 10.5, Facing: game.FacingNorth},
			{Slot: 1, X: 2.5, Y: 1.5, Facing: game.FacingEast},
			{Slot: 2, X: 1.5, Y: 2.5, Facing: game.FacingSouth},
			{Slot: 3, X: 0.5, Y: 1.5, Facing: game.FacingWest},
		},
		Resources: game.Resources{
			Water:  game.ResourceState{Pool: game.ResourcePool{Current: 50, Max: 100}, Stash: game.ResourceStash{Current: 5, Max: 29}},
			Oxygen: game.ResourceState{Pool: game.ResourcePool{Current: 50, Max: 100}, Stash: game.ResourceStash{Current: 5, Max: 29}},
			Energy: game.ResourceState{Pool: game.ResourcePool{Current: 50, Max: 100}, Stash: game.ResourceStash{Current: 5, Max: 29}},
			Food:   game.ResourceState{Pool: game.ResourcePool{Current: 40, Max: 80}, Stash: game.ResourceStash{Current: 5, Max: 29}},
		},
	}
}

func TestEndToEndLobbyToMatchEnd(t *testing.T) {
	cfg := testConfig()
	manager := match.NewManager(match.Options{
		MaxMatches:         cfg.MaxMatches,
		MaxPlayersPerMatch: cfg.MaxPlayersPerMatch,
		Grid:               cfg.Rules.Grid,
		GameConfig:         game.DefaultConfig(),
		GenerateWorld: func(_ int64, teamSize int, _ game.Config) (game.World, error) {
			w := winWorld()
			w.TeamSize = teamSize
			return w, nil
		},
		NewSeed: func() (int64, error) { return 1, nil },
	})
	s := newTestServerWithManager(t, cfg, manager)

	host := dialWS(t, s)
	readWelcome(t, host)
	hello(t, host, "hello-host", "ana")

	created := createMatch(t, host, "create-1")
	if created.MatchID == "" {
		t.Fatalf("unexpected create result: %+v", created)
	}

	guest := dialWS(t, s)
	readWelcome(t, guest)
	hello(t, guest, "hello-guest", "bo")
	joined := joinMatchByID(t, guest, "join-1", created.MatchID)
	if joined.State.TeamSize != 2 {
		t.Fatalf("joined team size = %d, want 2", joined.State.TeamSize)
	}
	_ = expectState(t, host)

	startMatch(t, host, "start-1")
	_, _ = expectStartStates(t, guest)

	// Slot 0 spawns on the stash; collect must transfer stash into pools.
	writeEnvelope(t, host, protocol.Envelope{ID: "collect-1", Type: protocol.TypeStashCollect})
	collectEnv := readEnvelopeOfType(t, host, protocol.TypeStashCollect)
	collect := decodePayload[protocol.StashCollectResult](t, collectEnv)
	if collect.Collected.Water != 5 || collect.Collected.Oxygen != 5 || collect.Collected.Energy != 5 {
		t.Fatalf("collect result = %+v, want 5 of each", collect)
	}

	// One build in front (north to the investigation cell) covers the goal.
	writeEnvelope(t, host, protocol.Envelope{
		ID:      "build-1",
		Type:    protocol.TypeBaseBuild,
		Payload: mustRaw(t, protocol.BaseBuildRequest{CellX: intPtr(10), CellY: intPtr(9)}),
	})
	var buildEnv, endedEnv protocol.Envelope
	for buildEnv.Type == "" || endedEnv.Type == "" {
		env := readEnvelope(t, host)
		if env.Type == protocol.TypeMatchFrame {
			continue
		}
		switch env.Type {
		case protocol.TypeBaseBuild:
			buildEnv = env
		case protocol.TypeMatchEnded:
			endedEnv = env
		default:
			t.Fatalf("unexpected envelope %q", env.Type)
		}
	}
	build := decodePayload[protocol.BaseBuildResult](t, buildEnv)
	if build.Construction.CellX != 10 || build.Construction.CellY != 9 {
		t.Fatalf("build construction = %+v, want (10,9)", build.Construction)
	}

	ended := decodePayload[protocol.MatchEnded](t, endedEnv)
	if ended.Outcome != protocol.OutcomeWin || ended.Reason != protocol.ReasonAllInvestigated {
		t.Fatalf("ended = %+v, want win/all_investigated", ended)
	}
	if ended.Stats.InvestigationCovered != 1 || ended.Stats.InvestigationTotal != 1 {
		t.Fatalf("ended stats = %+v", ended.Stats)
	}

	// Guest receives match.build (reliable) then match.ended.
	var guestEnded protocol.Envelope
	for guestEnded.Type == "" {
		env := readEnvelope(t, guest)
		if env.Type == protocol.TypeMatchFrame || env.Type == protocol.TypeMatchBuild {
			continue
		}
		if env.Type != protocol.TypeMatchEnded {
			t.Fatalf("unexpected guest envelope %q", env.Type)
		}
		guestEnded = env
	}
	guestResult := decodePayload[protocol.MatchEnded](t, guestEnded)
	if guestResult.Outcome != protocol.OutcomeWin {
		t.Fatalf("guest ended = %+v, want win", guestResult)
	}

}

func TestEconomyTicksCollectNearStashAndDepletionLoss(t *testing.T) {
	cfg := testConfig()
	manager := match.NewManager(match.Options{
		MaxMatches:         cfg.MaxMatches,
		MaxPlayersPerMatch: cfg.MaxPlayersPerMatch,
		Grid:               cfg.Rules.Grid,
		GameConfig:         game.DefaultConfig(),
		GenerateWorld: func(_ int64, teamSize int, _ game.Config) (game.World, error) {
			w := winWorld()
			w.TeamSize = teamSize
			// Place stash at (11,11) and slot 0 right next to its north edge (11.5, 10.95 -> distance 0.55 <= 0.70).
			w.Constructions[0] = game.Construction{ID: "b10-10", Kind: game.ConstructionBaseTile, CellX: 10, CellY: 10}
			w.Constructions[4] = game.Construction{ID: "stash", Kind: game.ConstructionStash, CellX: 11, CellY: 11}
			w.Spawns[0] = game.PlayerSpawn{Slot: 0, X: 11.5, Y: 10.95, Facing: game.FacingSouth}
			w.Resources.Water = game.ResourceState{
				Pool:  game.ResourcePool{Current: 1.5, Max: 50},
				Stash: game.ResourceStash{Current: 0, Max: 20, GPS: 4},
				CPS:   6,
			}
			w.Resources.Oxygen = game.ResourceState{Pool: game.ResourcePool{Current: 50, Max: 50}, Stash: game.ResourceStash{Max: 20}}
			w.Resources.Energy = game.ResourceState{Pool: game.ResourcePool{Current: 50, Max: 50}, Stash: game.ResourceStash{Max: 20}}
			w.Resources.Food = game.ResourceState{Pool: game.ResourcePool{Current: 50, Max: 50}, Stash: game.ResourceStash{Max: 20}}
			return w, nil
		},
		NewSeed: func() (int64, error) { return 1, nil },
	})
	s := newTestServerWithManager(t, cfg, manager)

	conn := dialWS(t, s)
	readWelcome(t, conn)
	hello(t, conn, "h1", "ana")
	createMatch(t, conn, "c1")
	startMatch(t, conn, "s1")

	// Wait a couple frames for GPS/CPS to advance before collecting.
	time.Sleep(160 * time.Millisecond)

	writeEnvelope(t, conn, protocol.Envelope{ID: "col-1", Type: protocol.TypeStashCollect})
	var colEnv protocol.Envelope
	for colEnv.Type == "" {
		env := readEnvelope(t, conn)
		if env.Type == protocol.TypeMatchFrame {
			continue
		}
		if env.Type == protocol.TypeStashCollect {
			colEnv = env
			break
		}
		t.Fatalf("unexpected envelope before collect: %q (%s)", env.Type, string(env.Payload))
	}
	col := decodePayload[protocol.StashCollectResult](t, colEnv)
	if col.Collected.Water <= 0 {
		t.Fatalf("expected stash water > 0 from GPS ticking, got %+v", col)
	}

	// Keep reading until water pool drains to 0 and server emits match.ended loss.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		env := readEnvelope(t, conn)
		if env.Type == protocol.TypeMatchFrame {
			continue
		}
		if env.Type == protocol.TypeMatchEnded {
			ended := decodePayload[protocol.MatchEnded](t, env)
			if ended.Outcome != protocol.OutcomeLoss || ended.Reason != protocol.ReasonResourceDepleted {
				t.Fatalf("ended = %+v, want loss/resource_depleted", ended)
			}
			return
		}
	}
	t.Fatal("timed out waiting for depletion match.ended")
}
