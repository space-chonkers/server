package match

import (
	"fmt"
	"time"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

// Step advances every playing match to the manager clock: phenomenon roll,
// economy, then loss check. It is called from the server frame loop.
func (m *Manager) Step() error {
	now := m.opts.Now()
	m.mu.RLock()
	matches := make([]*Match, 0, len(m.matches))
	for _, match := range m.matches {
		matches = append(matches, match)
	}
	m.mu.RUnlock()

	var firstErr error
	for _, match := range matches {
		if err := match.step(now, m.opts.GameConfig); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("step match %s: %w", match.id, err)
		}
	}
	return firstErr
}

// Fix pays energy to clear the player's active phenomenon at the stash. now
// stamps the end-of-match stats; the roll timer restarts from the manager
// clock.
func (m *Manager) Fix(
	playerID string,
	now int64,
) (protocol.PhenomenonFixResult, *protocol.MatchEnded, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	match := m.matchOf(playerID)
	if match == nil {
		return protocol.PhenomenonFixResult{}, nil,
			protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in a match", playerID)
	}
	return match.fix(playerID, m.opts.GameConfig, now, m.opts.Now())
}

// step applies one simulation step at now. The first step after start arms the
// clocks; the first roll comes FirstRollDelaySeconds later. Every roll,
// including a none roll, resets the roll timer.
func (m *Match) step(now time.Time, config game.Config) error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.Lock()
	if m.phase != protocol.PhasePlaying || m.ended != nil || m.phenomenonRandom == nil {
		m.mu.Unlock()
		return nil
	}
	rollInterval := rollDuration(config)
	if m.lastStepAt.IsZero() {
		m.lastStepAt = now
		m.nextRollAt = now.Add(secondsDuration(config.Phenomena.FirstRollDelaySeconds))
		m.mu.Unlock()
		return nil
	}
	dt := max(now.Sub(m.lastStepAt).Seconds(), 0)

	world := m.world
	active := m.phenomenon
	nextRollAt := m.nextRollAt
	var phenomenonEvent protocol.Envelope
	started := false
	if active.Type == game.PhenomenonNone && !now.Before(nextRollAt) {
		roll, err := game.RollPhenomenon(m.phenomenonRandom, config)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		var result game.PhenomenonRollResult
		world, active, result, err = game.ApplyPhenomenonRoll(world, active, roll, config, now.UnixMilli())
		if err != nil {
			m.mu.Unlock()
			return err
		}
		nextRollAt = now.Add(rollInterval)
		switch {
		case result.Started:
			started = true
			phenomenonEvent, err = phenomenonEnvelope(m.id, protocol.PhenomenonActionStarted, active)
		case result.Instant:
			phenomenonEvent, err = protocol.NewEvent(protocol.TypeMatchPhenomenon, protocol.MatchPhenomenon{
				MatchID: m.id,
				Action:  protocol.PhenomenonActionInstant,
				Phenomenon: protocol.PhenomenonRottenFood{
					StartedAtMs: now.UnixMilli(), FoodLoss: result.FoodLoss,
				},
			})
		}
		if err != nil {
			m.mu.Unlock()
			return err
		}
	}
	// A phenomenon that starts now takes effect from the next step.
	acting := active
	if started {
		acting = m.phenomenon
	} else if active.Type != game.PhenomenonNone {
		active.ElapsedSeconds += dt
		acting = active
	}
	if _, err := game.AdvanceEconomyWithPhenomenon(&world.Resources, acting, dt); err != nil {
		m.mu.Unlock()
		return err
	}

	ended := matchEnd(world, m.id, m.startedAt, now.UnixMilli())
	endEvent, err := matchEndedEvent(ended)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	m.world = world
	m.phenomenon = active
	m.lastStepAt = now
	m.nextRollAt = nextRollAt
	m.ended = ended
	members := m.membersLocked()
	m.mu.Unlock()

	publishToMembers(members, phenomenonEvent)
	publishToMembers(members, endEvent)
	return nil
}

// fix clears the active phenomenon and restarts the roll timer from clockNow.
// The fixer gets the result; other members get the resolved event.
func (m *Match) fix(
	playerID string,
	config game.Config,
	now int64,
	clockNow time.Time,
) (protocol.PhenomenonFixResult, *protocol.MatchEnded, error) {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.Lock()
	slot, err := m.actionPlayerLocked(playerID)
	if err != nil {
		m.mu.Unlock()
		return protocol.PhenomenonFixResult{}, nil, err
	}
	nextWorld, err := game.FixPhenomenon(m.world, movementState(m.players[slot]), m.phenomenon)
	if err != nil {
		m.mu.Unlock()
		return protocol.PhenomenonFixResult{}, nil, gameActionError(err)
	}

	resolved := game.ActivePhenomenon{}
	resolvedEvent, err := phenomenonEnvelope(m.id, protocol.PhenomenonActionResolved, resolved)
	if err != nil {
		m.mu.Unlock()
		return protocol.PhenomenonFixResult{}, nil, err
	}
	ended := matchEnd(nextWorld, m.id, m.startedAt, now)
	endEvent, err := matchEndedEvent(ended)
	if err != nil {
		m.mu.Unlock()
		return protocol.PhenomenonFixResult{}, nil, err
	}
	m.world = nextWorld
	m.phenomenon = resolved
	m.nextRollAt = clockNow.Add(rollDuration(config))
	m.ended = ended
	result := protocol.PhenomenonFixResult{
		Phenomenon: protocol.ActivePhenomenon{Value: protocolPhenomenon(resolved)},
		Resources:  m.resourcesLocked(nextWorld),
	}
	members := m.membersLocked()
	m.mu.Unlock()

	for _, member := range members {
		if member.PlayerID != playerID {
			member.Sink.Send(resolvedEvent)
		}
	}
	publishToMembers(members, endEvent)
	return result, ended, nil
}

// resourcesLocked returns resources with the active phenomenon applied. The
// caller holds mu.
func (m *Match) resourcesLocked(world game.World) protocol.Resources {
	return resourcesState(game.EffectiveResources(world.Resources, m.phenomenon))
}

// activePhenomenonLocked returns the active phenomenon in wire form. The
// caller holds mu.
func (m *Match) activePhenomenonLocked() protocol.ActivePhenomenon {
	return protocol.ActivePhenomenon{Value: protocolPhenomenon(m.phenomenon)}
}

func phenomenonEnvelope(
	matchID string,
	action protocol.MatchPhenomenonAction,
	active game.ActivePhenomenon,
) (protocol.Envelope, error) {
	return protocol.NewEvent(protocol.TypeMatchPhenomenon, protocol.MatchPhenomenon{
		MatchID:    matchID,
		Action:     action,
		Phenomenon: protocolPhenomenon(active),
	})
}

// protocolPhenomenon converts the active phenomenon to its wire type.
func protocolPhenomenon(active game.ActivePhenomenon) protocol.Phenomenon {
	switch active.Type {
	case game.PhenomenonSolarStorm:
		return protocol.PhenomenonSolar{
			StartedAtMs:           active.StartedAtMs,
			FixCostEnergy:         active.FixCostEnergy,
			GpsReductionPerSecond: active.GPSReductionPerSecond,
		}
	case game.PhenomenonWaterLeak:
		return protocol.PhenomenonWaterLeak{
			StartedAtMs: active.StartedAtMs, FixCostEnergy: active.FixCostEnergy,
			CpsMultiplier: active.CPSMultiplier,
		}
	case game.PhenomenonOxygenLoss:
		return protocol.PhenomenonOxygenLoss{
			StartedAtMs: active.StartedAtMs, FixCostEnergy: active.FixCostEnergy,
			CpsMultiplier: active.CPSMultiplier,
		}
	default:
		return protocol.PhenomenonNone{}
	}
}

func rollDuration(config game.Config) time.Duration {
	return secondsDuration(config.Phenomena.RollSeconds)
}

func secondsDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
