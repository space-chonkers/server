package match

import (
	"fmt"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

type worldFactory func(teamSize int) (game.World, error)

// start validates the host and phase, generates the world, and publishes the
// countdown and playing states as one transition.
func (m *Match) start(playerID string, newWorld worldFactory, now int64) error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.Lock()
	if !m.hasLocked(playerID) {
		m.mu.Unlock()
		return protocol.Errorf(
			protocol.CodeNotInMatch, "player %s is not in match %s", playerID, m.id,
		)
	}
	if m.hostIDLocked() != playerID {
		m.mu.Unlock()
		return protocol.Errorf(
			protocol.CodeNotHost, "player %s is not the host of match %s", playerID, m.id,
		)
	}
	if m.phase != protocol.PhaseLobby {
		m.mu.Unlock()
		return protocol.Errorf(protocol.CodeWrongPhase, "match %s is already %s", m.id, m.phase)
	}

	members := m.membersLocked()
	teamSize := len(members)
	world, err := newWorld(teamSize)
	if err == nil {
		err = m.validateWorldLocked(world, members)
	}
	if err != nil {
		m.mu.Unlock()
		return protocol.Errorf(protocol.CodeInternal, "generate match world: %v", err)
	}

	m.phase = protocol.PhasePlaying
	m.teamSize = teamSize
	m.world = world
	countdown := m.stateLocked(protocol.PhaseCountdown, now, now)
	playing := m.stateLocked(protocol.PhasePlaying, 0, now)
	m.mu.Unlock()

	if err := publishState(members, countdown); err != nil {
		return err
	}
	return publishState(members, playing)
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
