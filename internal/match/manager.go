package match

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

const (
	// idAlphabet omits I, O, 0, and 1 so match codes are unambiguous when read
	// aloud or typed.
	idAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	idLength   = 4
	idAttempts = 8
)

// WorldGenerator creates the immutable starting world for a match.
type WorldGenerator func(seed int64, teamSize int, config game.Config) (game.World, error)

// SeedSource returns the seed for one world.
type SeedSource func() (int64, error)

// Clock returns the current time for the simulation step.
type Clock func() time.Time

// Options are the manager limits and world inputs. GenerateWorld, NewSeed, and
// GameConfig are composed into one world factory at construction. Grid is the
// authoritative map size for lobby and playing state; generated worlds must
// match it. Now is the simulation clock read by Step; it defaults to time.Now.
type Options struct {
	MaxMatches         int
	MaxPlayersPerMatch int
	Grid               protocol.GridSize
	GameConfig         game.Config
	GenerateWorld      WorldGenerator
	NewSeed            SeedSource
	Now                Clock
}

// Manager owns the registry and every transition that changes it. Lock order is
// Manager, then Match publication, then Match state; Match never calls back
// into Manager.
type Manager struct {
	mu       sync.RWMutex
	matches  map[string]*Match
	opts     Options
	newWorld worldFactory
	closed   bool
}

// NewManager returns an empty registry with the given limits.
func NewManager(opts Options) *Manager {
	generate := opts.GenerateWorld
	if generate == nil {
		generate = game.GenerateWorld
	}
	seed := opts.NewSeed
	if seed == nil {
		seed = randomSeed
	}
	newWorld := func(teamSize int) (game.World, int64, error) {
		worldSeed, err := seed()
		if err != nil {
			return game.World{}, 0, err
		}
		world, err := generate(worldSeed, teamSize, opts.GameConfig)
		return world, worldSeed, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Manager{
		matches:  make(map[string]*Match),
		opts:     opts,
		newWorld: newWorld,
	}
}

// Create starts a lobby with playerID as its first member and host.
func (m *Manager) Create(requestID, playerID, name string, sink Sink, now int64) (*Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkJoinable(playerID); err != nil {
		return nil, err
	}
	return m.createLobby(requestID, playerID, name, sink, now)
}

// Join adds a player to an existing lobby.
func (m *Manager) Join(requestID, matchID, playerID, name string, sink Sink, now int64) (*Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkJoinable(playerID); err != nil {
		return nil, err
	}
	match, ok := m.matches[matchID]
	if !ok {
		return nil, protocol.Errorf(protocol.CodeMatchNotFound, "match %s not found", matchID)
	}
	if err := match.join(requestID, playerID, name, sink, now); err != nil {
		return nil, err
	}
	return match, nil
}

// JoinAny adds a player to a lobby with space, creating one when none is
// joinable.
func (m *Manager) JoinAny(requestID, playerID, name string, sink Sink, now int64) (*Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkJoinable(playerID); err != nil {
		return nil, err
	}
	for _, match := range m.matches {
		if !match.joinable() {
			continue
		}
		if err := match.join(requestID, playerID, name, sink, now); err != nil {
			return nil, err
		}
		return match, nil
	}
	return m.createLobby(requestID, playerID, name, sink, now)
}

// Start freezes the player's lobby and publishes its start states. Only the
// host may start, and only once.
func (m *Manager) Start(playerID string, now int64) error {
	m.mu.RLock()
	match := m.matchOf(playerID)
	m.mu.RUnlock()
	if match == nil {
		return protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in a match", playerID)
	}
	return match.start(playerID, m.newWorld, now)
}

// Leave removes the player and publishes the surviving match state.
func (m *Manager) Leave(playerID string, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	match := m.matchOf(playerID)
	if match == nil {
		return protocol.Errorf(protocol.CodeNotInMatch, "player %s is not in a match", playerID)
	}
	empty, err := match.leave(playerID, now)
	if err != nil {
		return err
	}
	if empty {
		delete(m.matches, match.id)
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
func (m *Manager) createLobby(requestID, playerID, name string, sink Sink, now int64) (*Match, error) {
	if len(m.matches) >= m.opts.MaxMatches {
		return nil, protocol.Errorf(protocol.CodeServerFull, "match limit %d reached", m.opts.MaxMatches)
	}

	id, err := m.newID()
	if err != nil {
		return nil, err
	}
	match := newMatch(id, m.opts.MaxPlayersPerMatch)
	match.grid = m.opts.Grid
	if err := match.join(requestID, playerID, name, sink, now); err != nil {
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

// randomSeed returns a non-deterministic seed for one world.
func randomSeed() (int64, error) {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return 0, protocol.Errorf(protocol.CodeInternal, "generate world seed: %v", err)
	}
	return int64(binary.LittleEndian.Uint64(buffer[:])), nil
}
