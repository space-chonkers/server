package match

import (
	"errors"
	"fmt"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

// InsufficientResourcesError reports which configured build costs are unpaid.
type InsufficientResourcesError struct {
	Missing []protocol.ResourceKey
}

func (e *InsufficientResourcesError) Error() string {
	return fmt.Sprintf("%s: %v", protocol.CodeInsufficientResources, e.Missing)
}

func (e *InsufficientResourcesError) Unwrap() error {
	return protocol.Errorf(protocol.CodeInsufficientResources, "%s", e.Missing)
}

// Collect transfers the player's stash resources into their pools.
func (m *Manager) Collect(
	playerID string,
	now int64,
) (protocol.StashCollectResult, *protocol.MatchEnded, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	match := m.matchOf(playerID)
	if match == nil {
		return protocol.StashCollectResult{}, nil,
			protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in a match", playerID)
	}
	return match.collect(playerID, now)
}

// Build adds a paid base tile at the requested cell.
func (m *Manager) Build(
	playerID string,
	target game.Cell,
	now int64,
) (protocol.BaseBuildResult, *protocol.MatchEnded, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	match := m.matchOf(playerID)
	if match == nil {
		return protocol.BaseBuildResult{}, nil,
			protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in a match", playerID)
	}
	return match.build(playerID, target, m.opts.GameConfig, now)
}

func (m *Match) collect(
	playerID string,
	now int64,
) (protocol.StashCollectResult, *protocol.MatchEnded, error) {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.Lock()
	slot, err := m.actionPlayerLocked(playerID)
	if err != nil {
		m.mu.Unlock()
		return protocol.StashCollectResult{}, nil, err
	}
	nextWorld, collected, err := game.CollectAtStash(m.world, movementState(m.players[slot]))
	if err != nil {
		m.mu.Unlock()
		return protocol.StashCollectResult{}, nil, gameActionError(err)
	}

	ended := matchEnd(nextWorld, m.id, m.startedAt, now)
	endEvent, err := matchEndedEvent(ended)
	if err != nil {
		m.mu.Unlock()
		return protocol.StashCollectResult{}, nil, err
	}
	m.world = nextWorld
	m.ended = ended
	result := protocol.StashCollectResult{
		Collected: protocolAmounts(collected.Collected),
		Lost:      protocolAmounts(collected.Lost),
		Resources: resourcesState(nextWorld.Resources),
	}
	members := m.membersLocked()
	m.mu.Unlock()

	publishToMembers(members, endEvent)
	return result, ended, nil
}

func (m *Match) build(
	playerID string,
	target game.Cell,
	config game.Config,
	now int64,
) (protocol.BaseBuildResult, *protocol.MatchEnded, error) {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.Lock()
	slot, err := m.actionPlayerLocked(playerID)
	if err != nil {
		m.mu.Unlock()
		return protocol.BaseBuildResult{}, nil, err
	}
	nextWorld, built, err := game.BuildBaseTile(
		m.world, movementState(m.players[slot]), target, config,
	)
	if err != nil {
		m.mu.Unlock()
		return protocol.BaseBuildResult{}, nil, gameActionError(err)
	}

	resources := resourcesState(nextWorld.Resources)
	construction := protocol.Construction{
		ID: built.Construction.ID, Kind: protocol.ConstructionKind(built.Construction.Kind),
		CellX: built.Construction.CellX, CellY: built.Construction.CellY,
	}
	buildEvent, err := protocol.NewEvent(protocol.TypeMatchBuild, protocol.MatchBuild{
		MatchID: m.id, Construction: construction, Resources: resources,
	})
	if err != nil {
		m.mu.Unlock()
		return protocol.BaseBuildResult{}, nil, err
	}

	ended := matchEnd(nextWorld, m.id, m.startedAt, now)
	endEvent, err := matchEndedEvent(ended)
	if err != nil {
		m.mu.Unlock()
		return protocol.BaseBuildResult{}, nil, err
	}
	m.world = nextWorld
	m.ended = ended
	members := m.membersLocked()
	m.mu.Unlock()

	for _, member := range members {
		if member.PlayerID != playerID {
			member.Sink.Send(buildEvent)
		}
	}
	publishToMembers(members, endEvent)

	return protocol.BaseBuildResult{Construction: construction, Resources: resources}, ended, nil
}

func (m *Match) actionPlayerLocked(playerID string) (int, error) {
	if m.ended != nil {
		return 0, protocol.Errorf(protocol.CodeMatchOver, "match %s has ended", m.id)
	}
	if m.phase != protocol.PhasePlaying {
		return 0, protocol.Errorf(protocol.CodeWrongPhase, "match %s is not playing", m.id)
	}
	for slot, member := range m.members {
		if member != nil && member.PlayerID == playerID {
			if err := m.initializeMovementPlayersLocked(); err != nil {
				return 0, protocol.Errorf(protocol.CodeInternal, "initialize player state: %v", err)
			}
			return slot, nil
		}
	}
	return 0, protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in match %s", playerID, m.id)
}

func movementState(player protocol.PlayerState) game.PlayerMovementState {
	return game.PlayerMovementState{
		X: player.X, Y: player.Y, VX: player.VX, VY: player.VY,
		Facing: game.Facing(player.Facing),
	}
}

func gameActionError(err error) error {
	switch {
	case errors.Is(err, game.ErrNotOnBase):
		return protocol.Errorf(protocol.CodeNotOnBase, "%v", err)
	case errors.Is(err, game.ErrNotAtStash):
		return protocol.Errorf(protocol.CodeNotAtStash, "%v", err)
	case errors.Is(err, game.ErrOutOfBounds):
		return protocol.Errorf(protocol.CodeOutOfBounds, "%v", err)
	case errors.Is(err, game.ErrNotInFront):
		return protocol.Errorf(protocol.CodeNotInFront, "%v", err)
	case errors.Is(err, game.ErrCellOccupied):
		return protocol.Errorf(protocol.CodeCellOccupied, "%v", err)
	case errors.Is(err, game.ErrInsufficientResource):
		var insufficient *game.InsufficientResourcesError
		if errors.As(err, &insufficient) {
			missing := make([]protocol.ResourceKey, len(insufficient.Missing))
			for i, resource := range insufficient.Missing {
				missing[i] = protocol.ResourceKey(resource)
			}
			return &InsufficientResourcesError{Missing: missing}
		}
		return protocol.Errorf(protocol.CodeInsufficientResources, "%v", err)
	default:
		return err
	}
}

func matchEnd(world game.World, matchID string, startedAt, now int64) *protocol.MatchEnded {
	if game.AllInvestigationCovered(world) {
		return endedResult(world, matchID, startedAt, now,
			protocol.OutcomeWin, protocol.ReasonAllInvestigated, "")
	}
	for _, resource := range []struct {
		key   protocol.ResourceKey
		state game.ResourceState
	}{
		{protocol.ResourceWater, world.Resources.Water},
		{protocol.ResourceOxygen, world.Resources.Oxygen},
		{protocol.ResourceEnergy, world.Resources.Energy},
		{protocol.ResourceFood, world.Resources.Food},
	} {
		if resource.state.Pool.Current <= 0 {
			return endedResult(world, matchID, startedAt, now,
				protocol.OutcomeLoss, protocol.ReasonResourceDepleted, resource.key)
		}
	}
	return nil
}

func endedResult(
	world game.World,
	matchID string,
	startedAt, now int64,
	outcome protocol.MatchOutcome,
	reason protocol.MatchEndReason,
	depleted protocol.ResourceKey,
) *protocol.MatchEnded {
	covered := 0
	for _, goal := range world.Investigation {
		for _, construction := range world.Constructions {
			if construction.CellX == goal.CellX && construction.CellY == goal.CellY {
				covered++
				break
			}
		}
	}
	elapsed := now - startedAt
	if elapsed < 0 {
		elapsed = 0
	}
	return &protocol.MatchEnded{
		MatchID: matchID, EndedAtMs: now, Outcome: outcome, Reason: reason,
		Stats: protocol.MatchStats{
			ElapsedMs: elapsed, BaseCells: world.BaseCellCount,
			InvestigationCovered: covered, InvestigationTotal: len(world.Investigation),
			Remaining: protocolAmounts(game.ResourceAmounts{
				Water:  world.Resources.Water.Pool.Current,
				Oxygen: world.Resources.Oxygen.Pool.Current,
				Energy: world.Resources.Energy.Pool.Current,
				Food:   world.Resources.Food.Pool.Current,
			}),
			DepletedResource: depleted,
		},
	}
}

func matchEndedEvent(ended *protocol.MatchEnded) (protocol.Envelope, error) {
	if ended == nil {
		return protocol.Envelope{}, nil
	}
	return protocol.NewEvent(protocol.TypeMatchEnded, ended)
}

func publishToMembers(members []Member, event protocol.Envelope) {
	if event.Type == "" {
		return
	}
	for _, member := range members {
		member.Sink.Send(event)
	}
}

func protocolAmounts(amounts game.ResourceAmounts) protocol.ResourceAmounts {
	return protocol.ResourceAmounts{
		Water: amounts.Water, Oxygen: amounts.Oxygen,
		Energy: amounts.Energy, Food: amounts.Food,
	}
}
