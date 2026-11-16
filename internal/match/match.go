// Package match owns match lifecycle: creation, membership, slots, host
// migration, and per-match event fan-out. It starts no goroutines.
package match

import (
	"sync"
	"time"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

// Sink delivers one server message to a client. Send must not block or call
// back into the match; state publication holds the per-match order while
// calling it.
type Sink interface {
	Send(protocol.Envelope)
}

// Member is one player in a match.
type Member struct {
	PlayerID string
	Name     string
	Slot     int
	Sink     Sink
}

// Match is one lobby or running match. mu guards membership and the start
// snapshot; publishMu orders state publication against membership changes.
type Match struct {
	mu        sync.RWMutex
	publishMu sync.Mutex
	id        string
	members   []*Member // index == slot; nil is free
	hostSlot  int       // -1 when the match is empty
	phase     protocol.Phase
	grid      protocol.GridSize // published while the match is a lobby
	teamSize  int               // frozen when the match starts
	startedAt int64
	ended     *protocol.MatchEnded
	world     game.World
	players   []protocol.PlayerState
	tick      int64

	// Phenomenon state. The stream is seeded from the world seed at start;
	// lastStepAt and nextRollAt are set by the first Step while playing.
	phenomenonRandom *game.PhenomenonRandom
	phenomenon       game.ActivePhenomenon
	lastStepAt       time.Time
	nextRollAt       time.Time
}

func newMatch(id string, maxPlayers int) *Match {
	return &Match{
		id:       id,
		members:  make([]*Member, maxPlayers),
		hostSlot: -1,
		phase:    protocol.PhaseLobby,
	}
}

// ID returns the match code.
func (m *Match) ID() string { return m.id }

// add places a player in the lowest free slot and returns it. The first member
// becomes the host. The sink must be non-nil.
func (m *Match) add(playerID, name string, sink Sink) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addLocked(playerID, name, sink)
}

// addLocked places a player in the lowest free slot. The caller holds mu.
func (m *Match) addLocked(playerID, name string, sink Sink) (int, error) {
	free := -1
	for slot, member := range m.members {
		if member == nil {
			if free < 0 {
				free = slot
			}
			continue
		}
		if member.PlayerID == playerID {
			return 0, protocol.Errorf(protocol.CodeAlreadyInMatch, "player %s is already in match %s", playerID, m.id)
		}
	}
	if m.phase != protocol.PhaseLobby {
		return 0, protocol.Errorf(protocol.CodeMatchInProgress, "match %s has already started", m.id)
	}
	if free < 0 {
		return 0, protocol.Errorf(protocol.CodeMatchFull, "match %s is full", m.id)
	}

	m.members[free] = &Member{PlayerID: playerID, Name: name, Slot: free, Sink: sink}
	if m.hostSlot < 0 {
		m.hostSlot = free
	}
	return free, nil
}

// remove drops a player and reports whether they were found and whether the
// match is now empty. A departing host promotes the lowest occupied slot.
func (m *Match) remove(playerID string) (removed, empty bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.removeLocked(playerID)
}

// removeLocked drops a player. The caller holds mu.
func (m *Match) removeLocked(playerID string) (removed, empty bool) {
	for slot, member := range m.members {
		if member == nil || member.PlayerID != playerID {
			continue
		}
		m.members[slot] = nil
		if slot == m.hostSlot {
			m.hostSlot = -1
			for next, candidate := range m.members {
				if candidate != nil {
					m.hostSlot = next
					break
				}
			}
		}
		return true, m.empty()
	}
	return false, m.empty()
}

// has reports whether playerID is a member.
func (m *Match) has(playerID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hasLocked(playerID)
}

// joinable reports whether the match is still in its lobby and has a free
// slot.
func (m *Match) joinable() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.phase != protocol.PhaseLobby {
		return false
	}
	for _, member := range m.members {
		if member == nil {
			return true
		}
	}
	return false
}

// empty reports whether the match has no members. The caller holds mu.
func (m *Match) empty() bool {
	for _, member := range m.members {
		if member != nil {
			return false
		}
	}
	return true
}

// Members returns a copy sorted by slot.
func (m *Match) Members() []Member {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.membersLocked()
}

// HostID returns the current host, or an empty string when the match is empty.
func (m *Match) HostID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hostIDLocked()
}

// membersLocked returns a copy in slot order. The caller holds mu.
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
