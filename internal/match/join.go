package match

import "github.com/space-chonkers/server/internal/protocol"

// join adds a player and publishes the lobby state. The joiner receives the
// match.join response; existing members receive match.state.
func (m *Match) join(requestID, playerID, name string, sink Sink, now int64) error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.Lock()
	if _, err := m.addLocked(playerID, name, sink); err != nil {
		m.mu.Unlock()
		return err
	}
	state := m.stateLocked(m.phase, 0, now)
	members := m.membersLocked()
	m.mu.Unlock()

	return publishJoin(members, requestID, playerID, state)
}

// leave removes a player and publishes the updated state. It reports whether
// the match became empty.
func (m *Match) leave(playerID string, now int64) (bool, error) {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	m.mu.Lock()
	removed, empty := m.removeLocked(playerID)
	if !removed {
		m.mu.Unlock()
		return false, protocol.Errorf(
			protocol.CodeNotInMatch, "player %s is not in match %s", playerID, m.id,
		)
	}
	if empty {
		m.mu.Unlock()
		return true, nil
	}
	state := m.stateLocked(m.phase, 0, now)
	members := m.membersLocked()
	m.mu.Unlock()

	return false, publishState(members, state)
}
