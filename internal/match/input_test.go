package match

import (
	"math"
	"testing"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

func movementRequest(seq int64, x, y *float64) *protocol.MatchInputRequest {
	request := &protocol.MatchInputRequest{Seq: &seq}
	if x != nil && y != nil {
		request.Move = &protocol.InputMove{X: x, Y: y}
	}
	return request
}

func inputTestBase() []game.Construction {
	base := make([]game.Construction, 0, 9)
	for y := 10; y < 13; y++ {
		for x := 10; x < 13; x++ {
			base = append(base, game.Construction{
				ID: "base", Kind: game.ConstructionBaseTile, CellX: x, CellY: y,
			})
		}
	}
	return base
}

func TestApplyPlayerInputUpdatesStateAndSequence(t *testing.T) {
	player := &protocol.PlayerState{
		Facing: protocol.FacingNorth, X: 11.5, Y: 11.5, LastInputSeq: -1,
	}
	x, y := 0.5, -1.0
	applied, err := ApplyPlayerInput(player, movementRequest(0, &x, &y), inputTestBase(), 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("new input was not applied")
	}
	if math.Abs(player.X-(11.5+0.25/math.Sqrt(1.25)*2)) > 1e-12 ||
		math.Abs(player.Y-(11.5-0.25/math.Sqrt(1.25)*4)) > 1e-12 {
		t.Fatalf("position = (%v,%v), unexpected result", player.X, player.Y)
	}
	if player.Facing != protocol.FacingNorth {
		t.Fatalf("facing = %q, want north", player.Facing)
	}
	if player.LastInputSeq != 0 {
		t.Fatalf("last input seq = %d, want 0", player.LastInputSeq)
	}
}

func TestApplyPlayerInputOmittedMoveStopsAndRetainsFacing(t *testing.T) {
	player := &protocol.PlayerState{
		Facing: protocol.FacingWest, X: 11.5, Y: 11.5, VX: 4, LastInputSeq: 3,
	}
	applied, err := ApplyPlayerInput(player, movementRequest(4, nil, nil), inputTestBase(), 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("new stop input was not applied")
	}
	if player.X != 11.5 || player.Y != 11.5 || player.VX != 0 || player.VY != 0 {
		t.Fatalf("state after stop input = %+v", player)
	}
	if player.Facing != protocol.FacingWest {
		t.Fatalf("facing = %q, want west", player.Facing)
	}
}

func TestApplyPlayerInputIgnoresStaleAndDuplicateSequences(t *testing.T) {
	player := &protocol.PlayerState{
		Facing: protocol.FacingNorth, X: 11.5, Y: 11.5, LastInputSeq: 5,
	}
	for _, seq := range []int64{4, 5} {
		x, y := 1.0, 0.0
		applied, err := ApplyPlayerInput(player, movementRequest(seq, &x, &y), inputTestBase(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if applied {
			t.Fatalf("sequence %d was applied", seq)
		}
	}
	if player.X != 11.5 || player.Y != 11.5 || player.Facing != protocol.FacingNorth ||
		player.VX != 0 || player.VY != 0 || player.LastInputSeq != 5 {
		t.Fatalf("stale input mutated player: %+v", player)
	}
}

func TestApplyPlayerInputRejectsInvalidRequestWithoutMutation(t *testing.T) {
	player := &protocol.PlayerState{
		Facing: protocol.FacingNorth, X: 11.5, Y: 11.5, LastInputSeq: -1,
	}
	x := 1.0
	request := movementRequest(0, nil, nil)
	request.Move = &protocol.InputMove{X: &x}
	if applied, err := ApplyPlayerInput(player, request, inputTestBase(), 0.25); err == nil || applied {
		t.Fatalf("ApplyPlayerInput() = %v, %v; want error and no application", applied, err)
	}
	if player.X != 11.5 || player.Y != 11.5 || player.LastInputSeq != -1 {
		t.Fatalf("invalid input mutated player: %+v", player)
	}
}
