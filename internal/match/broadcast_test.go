package match

import (
	"encoding/json"
	"testing"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

func TestBroadcastFrameSkipsLobby(t *testing.T) {
	manager := newTestManager(t, nil)
	sink := &fakeSink{}
	match := mustCreate(t, manager, "p_1", sink)

	before := sink.count()
	if err := match.BroadcastFrame(); err != nil {
		t.Fatalf("broadcast lobby frame: %v", err)
	}
	if got := sink.count(); got != before {
		t.Fatalf("lobby received %d frames, want 0", got-before)
	}
}

func TestStartedMatchFramesIncludePlayers(t *testing.T) {
	manager := newTestManager(t, nil)
	sink := &fakeSink{}
	match := mustCreate(t, manager, "p_1", sink)
	mustStart(t, manager, "p_1")

	if err := match.BroadcastFrame(); err != nil {
		t.Fatalf("broadcast frame: %v", err)
	}
	envelope, ok := sink.last()
	if !ok || envelope.Type != protocol.TypeMatchFrame {
		t.Fatalf("last envelope = %+v, want match.frame", envelope)
	}
	var frame protocol.MatchFrame
	if err := json.Unmarshal(envelope.Payload, &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if len(frame.Players) != 1 || frame.Players[0].ID != "p_1" {
		t.Fatalf("frame players = %+v, want p_1 before any input", frame.Players)
	}
}

func TestStateUsesLivePlayerMovement(t *testing.T) {
	manager := newTestManager(t, func(_ int64, teamSize int, _ game.Config) (game.World, error) {
		world := testWorld(teamSize)
		world.Spawns[0].X = 5.5
		world.Spawns[0].Y = 5.5
		return world, nil
	})
	hostSink := &fakeSink{}
	match := mustCreate(t, manager, "p_1", hostSink)
	mustJoin(t, manager, match.ID(), "p_2", &fakeSink{})
	mustStart(t, manager, "p_1")

	seq := int64(1)
	x, y := 1.0, 0.0
	_, applied, err := manager.ApplyPlayerInput("p_1", &protocol.MatchInputRequest{
		Seq:  &seq,
		Move: &protocol.InputMove{X: &x, Y: &y},
	}, game.PlayerSpeed, 1.0/30)
	if err != nil || !applied {
		t.Fatalf("apply input = %v, %v", applied, err)
	}

	state := match.State(1)
	if state.Tick != seq {
		t.Fatalf("state tick = %d, want %d", state.Tick, seq)
	}
	moved := state.Players[0]
	if moved.LastInputSeq != seq || moved.X <= 5.5 || moved.Facing != protocol.FacingEast {
		t.Fatalf("state player = %+v, want live movement from spawn", moved)
	}
	if state.Players[1].LastInputSeq != -1 {
		t.Fatalf("guest input sequence = %d, want -1", state.Players[1].LastInputSeq)
	}

	if err := manager.Leave("p_2", 2); err != nil {
		t.Fatalf("leave guest: %v", err)
	}
	after := sinkState(t, hostSink, 4)
	if len(after.Players) != 1 || after.Players[0].ID != "p_1" {
		t.Fatalf("state after leave = %+v", after.Players)
	}
	if after.Players[0].X != moved.X || after.Players[0].Y != moved.Y || after.Players[0].LastInputSeq != seq {
		t.Fatalf("leave reset live movement: %+v, want %+v", after.Players[0], moved)
	}
}

func TestManagerBroadcastFramesOnlyPlayingMatches(t *testing.T) {
	manager := newTestManager(t, nil)
	lobbySink := &fakeSink{}
	playingSink := &fakeSink{}
	mustCreate(t, manager, "p_lobby", lobbySink)
	mustCreate(t, manager, "p_playing", playingSink)
	mustStart(t, manager, "p_playing")

	lobbyBefore := lobbySink.count()
	playingBefore := playingSink.count()
	if err := manager.BroadcastFrames(); err != nil {
		t.Fatalf("broadcast frames: %v", err)
	}

	if got := lobbySink.count(); got != lobbyBefore {
		t.Fatalf("lobby received %d frames, want 0", got-lobbyBefore)
	}
	if got := playingSink.count(); got != playingBefore+1 {
		t.Fatalf("playing match received %d frames, want 1", got-playingBefore)
	}
	if envelope, ok := playingSink.last(); !ok || envelope.Type != protocol.TypeMatchFrame {
		t.Fatalf("playing match last envelope = %+v, want match.frame", envelope)
	}
}
