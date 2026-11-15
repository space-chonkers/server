package match

import (
	"fmt"
	"time"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

func (m *Match) start(playerID string, seed int64, config game.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.hostSlot < 0 || m.members[m.hostSlot].PlayerID != playerID {
		return protocol.Errorf(protocol.CodeNotHost, "only the match host can start the match")
	}
	if m.phase != protocol.PhaseLobby {
		return protocol.Errorf(protocol.CodeWrongPhase, "match %s is not in the lobby phase", m.id)
	}

	teamSize := 0
	for _, member := range m.members {
		if member != nil {
			teamSize++
		}
	}
	world, err := game.GenerateWorld(seed, teamSize, config)
	if err != nil {
		return fmt.Errorf("generate match world: %w", err)
	}

	players := make([]protocol.PlayerState, len(m.members))
	for slot, member := range m.members {
		if member == nil {
			continue
		}
		spawn, ok := spawnForSlot(world.Spawns, slot)
		if !ok {
			return fmt.Errorf("world has no spawn for player slot %d", slot)
		}
		players[slot] = protocol.PlayerState{
			ID: member.PlayerID, Slot: slot, Name: member.Name,
			Facing: protocol.Facing(spawn.Facing),
			X:      spawn.X, Y: spawn.Y, LastInputSeq: -1,
		}
	}

	m.world = &world
	m.players = players
	m.phase = protocol.PhasePlaying
	m.tick = 0
	return nil
}

func spawnForSlot(spawns []game.PlayerSpawn, slot int) (game.PlayerSpawn, bool) {
	for _, spawn := range spawns {
		if spawn.Slot == slot {
			return spawn, true
		}
	}
	return game.PlayerSpawn{}, false
}

// State returns the current full match snapshot.
func (m *Match) State() protocol.MatchState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state := protocol.MatchState{
		MatchID: m.id, Schema: protocol.StateSchema, Tick: m.tick,
		ServerTimeMs: time.Now().UnixMilli(), Phase: m.phase,
		HostID: m.hostIDLocked(), Players: m.playerStatesLocked(),
		Grid:          protocol.GridSize{Width: 50, Height: 50},
		TeamSize:      m.teamSizeLocked(),
		Constructions: []protocol.Construction{},
		Investigation: []protocol.InvestigationCell{},
		Phenomenon: protocol.ActivePhenomenon{
			Value: protocol.PhenomenonNone{},
		},
	}
	if m.world == nil {
		return state
	}

	state.Grid = protocol.GridSize{Width: m.world.Grid.Width, Height: m.world.Grid.Height}
	state.BaseCellCount = m.world.BaseCellCount
	state.TeamSize = m.world.TeamSize
	state.Constructions = protocolConstructions(m.world.Constructions)
	state.Investigation = protocolInvestigation(m.world.Investigation)
	state.Resources = protocolResources(m.world.Resources)
	return state
}

// Frame returns the current lossy movement snapshot.
func (m *Match) Frame() protocol.MatchFrame {
	m.mu.RLock()
	defer m.mu.RUnlock()

	frame := protocol.MatchFrame{
		MatchID: m.id, Tick: m.tick, ServerTimeMs: time.Now().UnixMilli(),
		HostID: m.hostIDLocked(), Players: m.playerStatesLocked(),
		Resources: protocol.Resources{},
		Phenomenon: protocol.ActivePhenomenon{
			Value: protocol.PhenomenonNone{},
		},
	}
	if m.world != nil {
		frame.BaseCellCount = m.world.BaseCellCount
		frame.TeamSize = m.world.TeamSize
		frame.Resources = protocolResources(m.world.Resources)
	}
	return frame
}

// StartForPlayer starts the player's lobby if they are its host.
func (m *Manager) StartForPlayer(playerID string, seed int64, config game.Config) (*Match, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	match := m.matchOf(playerID)
	if match == nil {
		return nil, protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in a match", playerID)
	}
	if err := match.start(playerID, seed, config); err != nil {
		return nil, err
	}
	return match, nil
}

// LeavePlayer removes a player from whichever match currently contains them.
// It returns the match if other players remain.
func (m *Manager) LeavePlayer(playerID string) (*Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	match := m.matchOf(playerID)
	if match == nil {
		return nil, protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in a match", playerID)
	}
	removed, empty := match.remove(playerID)
	if !removed {
		return nil, protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in match %s", playerID, match.ID())
	}
	if empty {
		delete(m.matches, match.ID())
		return nil, nil
	}
	return match, nil
}

// ApplyPlayerInput applies the player's input to their current match.
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

func (m *Match) hostIDLocked() string {
	if m.hostSlot < 0 || m.members[m.hostSlot] == nil {
		return ""
	}
	return m.members[m.hostSlot].PlayerID
}

func (m *Match) teamSizeLocked() int {
	teamSize := 0
	for _, member := range m.members {
		if member != nil {
			teamSize++
		}
	}
	return teamSize
}

func (m *Match) playerStatesLocked() []protocol.PlayerState {
	players := make([]protocol.PlayerState, 0, len(m.members))
	for slot, member := range m.members {
		if member == nil {
			continue
		}
		if slot < len(m.players) && m.players[slot].ID != "" {
			players = append(players, m.players[slot])
			continue
		}
		players = append(players, protocol.PlayerState{
			ID: member.PlayerID, Slot: slot, Name: member.Name,
			Facing: protocol.FacingNorth, LastInputSeq: -1,
		})
	}
	return players
}

func protocolConstructions(constructions []game.Construction) []protocol.Construction {
	result := make([]protocol.Construction, len(constructions))
	for i, construction := range constructions {
		result[i] = protocol.Construction{
			ID: construction.ID, Kind: protocol.ConstructionKind(construction.Kind),
			CellX: construction.CellX, CellY: construction.CellY,
		}
	}
	return result
}

func protocolInvestigation(cells []game.Cell) []protocol.InvestigationCell {
	result := make([]protocol.InvestigationCell, len(cells))
	for i, cell := range cells {
		result[i] = protocol.InvestigationCell{CellX: cell.CellX, CellY: cell.CellY}
	}
	return result
}

func protocolResources(resources game.Resources) protocol.Resources {
	return protocol.Resources{
		Water:  protocolResource(resources.Water),
		Oxygen: protocolResource(resources.Oxygen),
		Energy: protocolResource(resources.Energy),
		Food:   protocolResource(resources.Food),
	}
}

func protocolResource(resource game.ResourceState) protocol.ResourceState {
	return protocol.ResourceState{
		Pool: protocol.ResourcePool{
			Current: resource.Pool.Current,
			Max:     resource.Pool.Max,
		},
		Stash: protocol.ResourceStash{
			Current: resource.Stash.Current,
			Max:     resource.Stash.Max,
			GPS:     resource.Stash.GPS,
		},
		CPS: resource.CPS,
	}
}
