package match

import "github.com/space-chonkers/server/internal/protocol"

// BroadcastFrame publishes the current snapshot to every member of a playing
// match. Lobby matches publish no frames.
func (m *Match) BroadcastFrame() error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.RLock()
	if m.phase != protocol.PhasePlaying {
		m.mu.RUnlock()
		return nil
	}
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

// BroadcastFrames publishes one frame for every playing match. The match
// snapshot is taken under the manager lock and released before publication.
func (m *Manager) BroadcastFrames() error {
	m.mu.RLock()
	matches := make([]*Match, 0, len(m.matches))
	for _, match := range m.matches {
		matches = append(matches, match)
	}
	m.mu.RUnlock()

	var firstErr error
	for _, match := range matches {
		if err := match.BroadcastFrame(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
