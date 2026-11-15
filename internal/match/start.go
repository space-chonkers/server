package match

import (
	"fmt"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

type worldFactory func(teamSize int) (game.World, error)

// start validates the host and phase, generates the world, and freezes the
// starting team.
func (m *Match) start(playerID string, newWorld worldFactory) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.hasLocked(playerID) {
		return protocol.Errorf(
			protocol.CodeNotInMatch, "player %s is not in match %s", playerID, m.id,
		)
	}
	if m.members[m.hostSlot].PlayerID != playerID {
		return protocol.Errorf(
			protocol.CodeNotHost, "player %s is not the host of match %s", playerID, m.id,
		)
	}
	if m.phase != protocol.PhaseLobby {
		return protocol.Errorf(protocol.CodeWrongPhase, "match %s is already %s", m.id, m.phase)
	}

	members := m.membersLocked()
	teamSize := len(members)
	world, err := newWorld(teamSize)
	if err != nil {
		return protocol.Errorf(protocol.CodeInternal, "generate match world: %v", err)
	}
	if err := m.validateWorldLocked(world, members); err != nil {
		return protocol.Errorf(protocol.CodeInternal, "generate match world: %v", err)
	}

	m.phase = protocol.PhasePlaying
	m.teamSize = teamSize
	m.world = world
	m.startPending = true
	return nil
}

// validateWorldLocked checks the generator's invariants against the lobby.
func (m *Match) validateWorldLocked(world game.World, members []Member) error {
	if world.TeamSize != len(members) {
		return fmt.Errorf("team size %d does not match lobby size %d", world.TeamSize, len(members))
	}
	if world.Grid.Width != m.grid.Width || world.Grid.Height != m.grid.Height {
		return fmt.Errorf("grid %dx%d does not match lobby grid %dx%d",
			world.Grid.Width, world.Grid.Height, m.grid.Width, m.grid.Height)
	}

	spawns := make(map[int]bool, len(world.Spawns))
	for _, spawn := range world.Spawns {
		if spawns[spawn.Slot] {
			return fmt.Errorf("duplicate spawn for slot %d", spawn.Slot)
		}
		spawns[spawn.Slot] = true
	}
	for _, member := range members {
		if !spawns[member.Slot] {
			return fmt.Errorf("missing spawn for slot %d", member.Slot)
		}
	}
	return nil
}
