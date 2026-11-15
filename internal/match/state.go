package match

import (
	"sort"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

// State returns the full state clients should render for the current phase.
// Lobby state reflects the current member count; playing state keeps the team
// size frozen at start.
func (m *Match) State(now int64) protocol.MatchState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateLocked(m.phase, 0, now)
}

// stateLocked builds one full state snapshot. The caller holds mu.
func (m *Match) stateLocked(phase protocol.Phase, phaseEndsAtMs, now int64) protocol.MatchState {
	if phase == protocol.PhaseLobby {
		return m.lobbyStateLocked(now)
	}
	return m.worldStateLocked(phase, phaseEndsAtMs, now)
}

func (m *Match) lobbyStateLocked(now int64) protocol.MatchState {
	members := m.membersLocked()
	players := make([]protocol.PlayerState, 0, len(members))
	for _, member := range members {
		players = append(players, protocol.PlayerState{
			ID:           member.PlayerID,
			Slot:         member.Slot,
			Name:         member.Name,
			Facing:       protocol.FacingNorth,
			LastInputSeq: -1,
		})
	}
	return protocol.MatchState{
		MatchID:      m.id,
		Schema:       protocol.StateSchema,
		ServerTimeMs: now,
		Phase:        protocol.PhaseLobby,
		HostID:       m.hostIDLocked(),
		Grid:         m.grid,
		TeamSize:     len(members),
		Players:      players,
		Phenomenon:   protocol.ActivePhenomenon{Value: protocol.PhenomenonNone{}},
	}
}

func (m *Match) worldStateLocked(phase protocol.Phase, phaseEndsAtMs, now int64) protocol.MatchState {
	members := m.membersLocked()
	spawns := make(map[int]game.PlayerSpawn, len(m.world.Spawns))
	for _, spawn := range m.world.Spawns {
		spawns[spawn.Slot] = spawn
	}

	players := make([]protocol.PlayerState, 0, len(members))
	for _, member := range members {
		spawn := spawns[member.Slot]
		players = append(players, protocol.PlayerState{
			ID:           member.PlayerID,
			Slot:         member.Slot,
			Name:         member.Name,
			Facing:       protocol.Facing(spawn.Facing),
			X:            spawn.X,
			Y:            spawn.Y,
			LastInputSeq: -1,
		})
	}

	constructions := make([]protocol.Construction, 0, len(m.world.Constructions))
	for _, construction := range m.world.Constructions {
		constructions = append(constructions, protocol.Construction{
			ID:    construction.ID,
			Kind:  protocol.ConstructionKind(construction.Kind),
			CellX: construction.CellX,
			CellY: construction.CellY,
		})
	}
	sort.Slice(constructions, func(i, j int) bool {
		if constructions[i].CellX == constructions[j].CellX {
			return constructions[i].CellY < constructions[j].CellY
		}
		return constructions[i].CellX < constructions[j].CellX
	})

	investigation := make([]protocol.InvestigationCell, 0, len(m.world.Investigation))
	for _, cell := range m.world.Investigation {
		investigation = append(investigation, protocol.InvestigationCell{
			CellX: cell.CellX,
			CellY: cell.CellY,
		})
	}
	sort.Slice(investigation, func(i, j int) bool {
		if investigation[i].CellX == investigation[j].CellX {
			return investigation[i].CellY < investigation[j].CellY
		}
		return investigation[i].CellX < investigation[j].CellX
	})

	return protocol.MatchState{
		MatchID:       m.id,
		Schema:        protocol.StateSchema,
		ServerTimeMs:  now,
		Phase:         phase,
		PhaseEndsAtMs: phaseEndsAtMs,
		HostID:        m.hostIDLocked(),
		Grid:          m.grid,
		BaseCellCount: m.world.BaseCellCount,
		TeamSize:      m.teamSize,
		Players:       players,
		Constructions: constructions,
		Investigation: investigation,
		Resources:     resourcesState(m.world.Resources),
		Phenomenon:    protocol.ActivePhenomenon{Value: protocol.PhenomenonNone{}},
	}
}

func resourcesState(resources game.Resources) protocol.Resources {
	return protocol.Resources{
		Water:  resourceState(resources.Water),
		Oxygen: resourceState(resources.Oxygen),
		Energy: resourceState(resources.Energy),
		Food:   resourceState(resources.Food),
	}
}

func resourceState(resource game.ResourceState) protocol.ResourceState {
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
