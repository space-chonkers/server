package match

import (
	"sort"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

// Started is the atomic result of a host starting a lobby. Countdown and
// Playing carry the same world snapshot so every member reconstructs the map
// from either state.
type Started struct {
	Match     *Match
	Countdown protocol.MatchState
	Playing   protocol.MatchState
}

// State returns the full state clients should render for the current phase.
// Lobby state reflects the current member count; playing state keeps the team
// size frozen at start.
func (m *Match) State(now int64) protocol.MatchState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	members := m.membersLocked()
	hostID := m.hostIDLocked()
	if m.phase == protocol.PhaseLobby {
		return lobbyState(m.id, members, hostID, m.grid, now)
	}
	return worldState(m.id, members, hostID, m.world, m.teamSize, m.phase, m.startedAtMs, now)
}

// start validates the host and phase, generates the world, and freezes the
// starting team. The caller holds the match lock.
func (m *Match) start(
	playerID string,
	seed SeedSource,
	generate WorldGenerator,
	config game.Config,
	now int64,
) (protocol.MatchState, protocol.MatchState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.hasLocked(playerID) {
		return protocol.MatchState{}, protocol.MatchState{}, protocol.Errorf(
			protocol.CodeNotInMatch, "player %s is not in match %s", playerID, m.id,
		)
	}
	if m.hostSlot < 0 || m.members[m.hostSlot].PlayerID != playerID {
		return protocol.MatchState{}, protocol.MatchState{}, protocol.Errorf(
			protocol.CodeNotHost, "player %s is not the host of match %s", playerID, m.id,
		)
	}
	if m.phase != protocol.PhaseLobby {
		return protocol.MatchState{}, protocol.MatchState{}, protocol.Errorf(
			protocol.CodeWrongPhase, "match %s is already %s", m.id, m.phase,
		)
	}

	members := m.membersLocked()
	teamSize := len(members)

	worldSeed, err := seed()
	if err != nil {
		return protocol.MatchState{}, protocol.MatchState{}, err
	}
	world, err := generate(worldSeed, teamSize, config)
	if err != nil {
		return protocol.MatchState{}, protocol.MatchState{}, protocol.Errorf(
			protocol.CodeInternal, "generate match world: %v", err,
		)
	}

	m.phase = protocol.PhasePlaying
	m.teamSize = teamSize
	m.world = world
	m.startedAtMs = now

	// The countdown is a zero-length transition in M1: both states describe the
	// same world, and playing follows immediately from the start handler.
	countdown := worldState(
		m.id, members, playerID, world, teamSize, protocol.PhaseCountdown, now, now,
	)
	playing := worldState(
		m.id, members, playerID, world, teamSize, protocol.PhasePlaying, 0, now,
	)
	return countdown, playing, nil
}

// membersLocked returns a copy sorted by slot. The caller holds mu.
func (m *Match) membersLocked() []Member {
	members := make([]Member, 0, len(m.members))
	for _, member := range m.members {
		if member != nil {
			members = append(members, *member)
		}
	}
	return members
}

// hostIDLocked returns the host id, or an empty string. The caller holds mu.
func (m *Match) hostIDLocked() string {
	if m.hostSlot < 0 {
		return ""
	}
	return m.members[m.hostSlot].PlayerID
}

// hasLocked reports whether playerID is a member. The caller holds mu.
func (m *Match) hasLocked(playerID string) bool {
	for _, member := range m.members {
		if member != nil && member.PlayerID == playerID {
			return true
		}
	}
	return false
}

func lobbyState(
	matchID string,
	members []Member,
	hostID string,
	grid protocol.GridSize,
	now int64,
) protocol.MatchState {
	players := make([]protocol.PlayerState, 0, len(members))
	for _, member := range members {
		players = append(players, protocol.PlayerState{
			ID:           member.PlayerID,
			Slot:         member.Slot,
			Name:         member.Name,
			Facing:       protocol.FacingNorth,
			X:            0,
			Y:            0,
			VX:           0,
			VY:           0,
			LastInputSeq: -1,
		})
	}
	return protocol.MatchState{
		MatchID:       matchID,
		Schema:        protocol.StateSchema,
		Tick:          0,
		ServerTimeMs:  now,
		Phase:         protocol.PhaseLobby,
		PhaseEndsAtMs: 0,
		HostID:        hostID,
		Grid:          grid,
		BaseCellCount: 0,
		TeamSize:      len(members),
		Players:       players,
		Constructions: []protocol.Construction{},
		Investigation: []protocol.InvestigationCell{},
		Resources:     protocol.Resources{},
		Phenomenon:    protocol.ActivePhenomenon{Value: protocol.PhenomenonNone{}},
	}
}

func worldState(
	matchID string,
	members []Member,
	hostID string,
	world game.World,
	teamSize int,
	phase protocol.Phase,
	phaseEndsAtMs int64,
	now int64,
) protocol.MatchState {
	spawns := make(map[int]game.PlayerSpawn, len(world.Spawns))
	for _, spawn := range world.Spawns {
		spawns[spawn.Slot] = spawn
	}

	players := make([]protocol.PlayerState, 0, len(members))
	for _, member := range members {
		spawn := spawns[member.Slot]
		facing := protocol.Facing(spawn.Facing)
		if facing == "" {
			facing = protocol.FacingNorth
		}
		players = append(players, protocol.PlayerState{
			ID:           member.PlayerID,
			Slot:         member.Slot,
			Name:         member.Name,
			Facing:       facing,
			X:            spawn.X,
			Y:            spawn.Y,
			VX:           0,
			VY:           0,
			LastInputSeq: -1,
		})
	}

	constructions := make([]protocol.Construction, 0, len(world.Constructions))
	for _, construction := range world.Constructions {
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

	investigation := make([]protocol.InvestigationCell, 0, len(world.Investigation))
	for _, cell := range world.Investigation {
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
		MatchID:       matchID,
		Schema:        protocol.StateSchema,
		Tick:          0,
		ServerTimeMs:  now,
		Phase:         phase,
		PhaseEndsAtMs: phaseEndsAtMs,
		HostID:        hostID,
		Grid:          protocol.GridSize{Width: world.Grid.Width, Height: world.Grid.Height},
		BaseCellCount: world.BaseCellCount,
		TeamSize:      teamSize,
		Players:       players,
		Constructions: constructions,
		Investigation: investigation,
		Resources:     resourcesState(world.Resources),
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
