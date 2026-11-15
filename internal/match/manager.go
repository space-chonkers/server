package match

import (
	"crypto/rand"
	"sync"

	"github.com/space-chonkers/server/internal/protocol"
)

const (
	// idAlphabet omits I, O, 0, and 1 so match codes are unambiguous when read
	// aloud or typed.
	idAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	idLength   = 4
	idAttempts = 8
)

// Options are the manager limits, normally from the server configuration. Both
// limits must be non-negative.
type Options struct {
	MaxMatches         int
	MaxPlayersPerMatch int
}

// Manager owns the registry and every transition that changes it. Lock order is
// Manager then Match; Match never calls back into Manager.
type Manager struct {
	mu      sync.RWMutex
	matches map[string]*Match
	opts    Options
	closed  bool
}

// NewManager returns an empty registry with the given limits.
func NewManager(opts Options) *Manager {
	return &Manager{
		matches: make(map[string]*Match),
		opts:    opts,
	}
}

// Create starts a lobby with playerID as its first member and host.
func (m *Manager) Create(playerID, name string, sink Sink) (*Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkJoinable(playerID); err != nil {
		return nil, err
	}
	return m.createLobby(playerID, name, sink)
}

// Join adds a player to an existing lobby.
func (m *Manager) Join(matchID, playerID, name string, sink Sink) (*Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkJoinable(playerID); err != nil {
		return nil, err
	}
	match, ok := m.matches[matchID]
	if !ok {
		return nil, protocol.Errorf(protocol.CodeMatchNotFound, "match %s not found", matchID)
	}
	if _, err := match.add(playerID, name, sink); err != nil {
		return nil, err
	}
	return match, nil
}

// JoinAny adds a player to the first lobby with space, creating one when none
// has room.
func (m *Manager) JoinAny(playerID, name string, sink Sink) (*Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkJoinable(playerID); err != nil {
		return nil, err
	}
	for _, match := range m.matches {
		if !match.available() {
			continue
		}
		if _, err := match.add(playerID, name, sink); err != nil {
			return nil, err
		}
		return match, nil
	}
	return m.createLobby(playerID, name, sink)
}

// Leave removes a player and drops the match when it becomes empty.
func (m *Manager) Leave(matchID, playerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	match, ok := m.matches[matchID]
	if !ok {
		return protocol.Errorf(protocol.CodeMatchNotFound, "match %s not found", matchID)
	}
	removed, empty := match.remove(playerID)
	if !removed {
		return protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in match %s", playerID, matchID)
	}
	if empty {
		delete(m.matches, matchID)
	}
	return nil
}

// Get returns a live match by code.
func (m *Manager) Get(id string) (*Match, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	match, ok := m.matches[id]
	return match, ok
}

// Delete drops a match and reports whether it existed.
func (m *Manager) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.matches[id]; !ok {
		return false
	}
	delete(m.matches, id)
	return true
}

// Count returns the number of live matches.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.matches)
}

// Close drops every match and refuses later joins and creates.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.matches = make(map[string]*Match)
}

// checkJoinable enforces the shutdown and one-match-per-player rules. The
// caller holds the manager lock.
func (m *Manager) checkJoinable(playerID string) error {
	if m.closed {
		return protocol.Errorf(protocol.CodeServerFull, "server is shutting down")
	}
	if m.matchOf(playerID) != nil {
		return protocol.Errorf(protocol.CodeAlreadyInMatch, "player %s is already in a match", playerID)
	}
	return nil
}

// createLobby registers a match with its creator. The caller holds the lock and
// has checked checkJoinable.
func (m *Manager) createLobby(playerID, name string, sink Sink) (*Match, error) {
	if len(m.matches) >= m.opts.MaxMatches {
		return nil, protocol.Errorf(protocol.CodeServerFull, "match limit %d reached", m.opts.MaxMatches)
	}

	id, err := m.newID()
	if err != nil {
		return nil, err
	}
	match := newMatch(id, m.opts.MaxPlayersPerMatch)
	if _, err := match.add(playerID, name, sink); err != nil {
		return nil, err
	}
	m.matches[id] = match
	return match, nil
}

// matchOf returns the match holding playerID, or nil. The caller holds the
// manager lock.
func (m *Manager) matchOf(playerID string) *Match {
	for _, match := range m.matches {
		if match.has(playerID) {
			return match
		}
	}
	return nil
}

// newID returns an unused match code. The caller holds the manager lock.
func (m *Manager) newID() (string, error) {
	var buffer [idLength]byte
	for attempt := 0; attempt < idAttempts; attempt++ {
		if _, err := rand.Read(buffer[:]); err != nil {
			return "", protocol.Errorf(protocol.CodeInternal, "generate match id: %v", err)
		}
		for i, b := range buffer {
			buffer[i] = idAlphabet[int(b)%len(idAlphabet)]
		}
		if _, exists := m.matches[string(buffer[:])]; !exists {
			return string(buffer[:]), nil
		}
	}
	return "", protocol.Errorf(protocol.CodeInternal, "generate unique match id")
}
