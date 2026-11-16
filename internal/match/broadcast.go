package match

import (
	"fmt"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

// BroadcastFrame publishes the current snapshot to every member of a playing
// match. Lobby matches publish no frames.
func (m *Match) BroadcastFrame() error {
	return m.TickAndBroadcast(0, 0)
}

// TickAndBroadcast advances shared resource economy for dtSeconds when the
// match is playing and not yet ended, publishes the frame, and emits
// match.ended if any pool reached zero.
func (m *Match) TickAndBroadcast(dtSeconds float64, now int64) error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.Lock()
	if m.phase != protocol.PhasePlaying || m.ended != nil {
		m.mu.Unlock()
		return nil
	}
	var endEvent protocol.Envelope
	if m.ended == nil && dtSeconds > 0 {
		if _, err := game.AdvanceEconomy(&m.world.Resources, dtSeconds); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("match %s: advance economy: %w", m.id, err)
		}
		if ended := matchEnd(m.world, m.id, m.startedAt, now); ended != nil {
			var err error
			endEvent, err = matchEndedEvent(ended)
			if err != nil {
				m.mu.Unlock()
				return fmt.Errorf("match %s: encode end event: %w", m.id, err)
			}
			m.ended = ended
		}
	}
	members := m.membersLocked()
	frame := m.frameLocked()
	m.mu.Unlock()

	event, err := protocol.NewEvent(protocol.TypeMatchFrame, frame)
	if err != nil {
		return err
	}
	for _, member := range members {
		member.Sink.Send(event)
	}
	publishToMembers(members, endEvent)
	return nil
}

// BroadcastFrames publishes one frame for every playing match.
func (m *Manager) BroadcastFrames() error {
	return m.TickAndBroadcastFrames(0, 0)
}

// TickAndBroadcastFrames advances economy and publishes one frame for every
// playing match. The match snapshot is taken under the manager lock and
// released before publication.
func (m *Manager) TickAndBroadcastFrames(dtSeconds float64, now int64) error {
	m.mu.RLock()
	matches := make([]*Match, 0, len(m.matches))
	for _, match := range m.matches {
		matches = append(matches, match)
	}
	m.mu.RUnlock()

	var firstErr error
	for _, match := range matches {
		if err := match.TickAndBroadcast(dtSeconds, now); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
