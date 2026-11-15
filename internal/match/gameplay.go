package match

import (
	"fmt"
	"time"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

// ApplyPlayerInput applies a newer input snapshot to the player's current match.
func (m *Manager) ApplyPlayerInput(
	playerID string,
	request *protocol.MatchInputRequest,
	speedPerSecond float64,
	elapsedSeconds float64,
) (*Match, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	match := m.matchOf(playerID)
	if match == nil {
		return nil, false, protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in a match", playerID)
	}
	applied, err := match.applyInput(playerID, request, speedPerSecond, elapsedSeconds)
	return match, applied, err
}

func (m *Match) initializeMovementPlayersLocked() error {
	if m.players != nil {
		return nil
	}

	spawns := make(map[int]game.PlayerSpawn, len(m.world.Spawns))
	for _, spawn := range m.world.Spawns {
		spawns[spawn.Slot] = spawn
	}
	players := make([]protocol.PlayerState, len(m.members))
	for slot, member := range m.members {
		if member == nil {
			continue
		}
		spawn, ok := spawns[slot]
		if !ok {
			return fmt.Errorf("world has no spawn for player slot %d", slot)
		}
		players[slot] = protocol.PlayerState{
			ID: member.PlayerID, Slot: slot, Name: member.Name,
			Facing: protocol.Facing(spawn.Facing),
			X:      spawn.X, Y: spawn.Y, LastInputSeq: -1,
		}
	}
	m.players = players
	return nil
}

// Frame returns the current lossy movement snapshot.
func (m *Match) Frame() protocol.MatchFrame {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.frameLocked()
}

func (m *Match) frameLocked() protocol.MatchFrame {
	players := make([]protocol.PlayerState, 0, len(m.members))
	for slot, member := range m.members {
		if member == nil || slot >= len(m.players) || m.players[slot].ID == "" {
			continue
		}
		players = append(players, m.players[slot])
	}

	return protocol.MatchFrame{
		MatchID:       m.id,
		Tick:          m.tick,
		ServerTimeMs:  time.Now().UnixMilli(),
		HostID:        m.hostIDLocked(),
		BaseCellCount: m.world.BaseCellCount,
		TeamSize:      m.teamSize,
		Players:       players,
		Resources:     resourcesState(m.world.Resources),
		Phenomenon:    protocol.ActivePhenomenon{Value: protocol.PhenomenonNone{}},
	}
}

// BroadcastFrame publishes the current movement snapshot in match order.
func (m *Match) BroadcastFrame() error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.RLock()
	members := m.membersLocked()
	frame := m.frameLocked()
	m.mu.RUnlock()

	event, err := protocol.NewEvent(protocol.TypeMatchFrame, frame)
	if err != nil {
		return err
	}
	for _, member := range members {
		member.Sink.Send(event)
	}
	return nil
}
