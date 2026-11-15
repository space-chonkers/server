package match

import (
	"fmt"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

func (m *Match) applyInput(
	playerID string,
	request *protocol.MatchInputRequest,
	speedPerSecond float64,
	elapsedSeconds float64,
) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.phase != protocol.PhasePlaying || m.world == nil {
		return false, protocol.Errorf(protocol.CodeWrongPhase, "match %s is not playing", m.id)
	}
	for slot, member := range m.members {
		if member == nil || member.PlayerID != playerID {
			continue
		}
		if slot >= len(m.players) {
			return false, fmt.Errorf("player slot %d has no movement state", slot)
		}
		applied, err := ApplyPlayerInputAtSpeed(
			&m.players[slot], request, m.world.Constructions, speedPerSecond, elapsedSeconds,
		)
		if err != nil || !applied {
			return applied, err
		}
		m.tick++
		return true, nil
	}
	return false, protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in match %s", playerID, m.id)
}

// ApplyPlayerInput applies a newer input snapshot to player. The caller owns
// synchronization for player and supplies the current walkable base cells.
// It returns false when the sequence is stale or duplicated.
func ApplyPlayerInput(
	player *protocol.PlayerState,
	request *protocol.MatchInputRequest,
	base []game.Construction,
	elapsedSeconds float64,
) (bool, error) {
	return ApplyPlayerInputAtSpeed(player, request, base, game.PlayerSpeed, elapsedSeconds)
}

// ApplyPlayerInputAtSpeed applies a newer input using a configured movement
// speed. The caller owns synchronization for player.
func ApplyPlayerInputAtSpeed(
	player *protocol.PlayerState,
	request *protocol.MatchInputRequest,
	base []game.Construction,
	speedPerSecond float64,
	elapsedSeconds float64,
) (bool, error) {
	if player == nil {
		return false, fmt.Errorf("player state is required")
	}
	if request == nil || request.Seq == nil {
		return false, fmt.Errorf("input request and sequence are required")
	}
	if *request.Seq < 0 {
		return false, fmt.Errorf("input sequence must be non-negative")
	}
	if request.Move != nil && (request.Move.X == nil || request.Move.Y == nil) {
		return false, fmt.Errorf("movement input requires both x and y")
	}
	if *request.Seq <= player.LastInputSeq {
		return false, nil
	}

	input := game.MovementInput{}
	if request.Move != nil {
		input.X = *request.Move.X
		input.Y = *request.Move.Y
	}
	movement, err := game.AdvancePlayerMovementAtSpeed(game.PlayerMovementState{
		X: player.X, Y: player.Y, VX: player.VX, VY: player.VY,
		Facing: game.Facing(player.Facing),
	}, input, base, speedPerSecond, elapsedSeconds)
	if err != nil {
		return false, err
	}

	player.X = movement.X
	player.Y = movement.Y
	player.VX = movement.VX
	player.VY = movement.VY
	player.Facing = protocol.Facing(movement.Facing)
	player.LastInputSeq = *request.Seq
	return true, nil
}
