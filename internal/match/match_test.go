package match

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/space-chonkers/server/internal/protocol"
)

// fakeSink records delivered envelopes for assertions.
type fakeSink struct {
	mu        sync.Mutex
	envelopes []protocol.Envelope
}

func (f *fakeSink) Send(envelope protocol.Envelope) {
	f.mu.Lock()
	f.envelopes = append(f.envelopes, envelope)
	f.mu.Unlock()
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.envelopes)
}

func (f *fakeSink) last() (protocol.Envelope, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.envelopes) == 0 {
		return protocol.Envelope{}, false
	}
	return f.envelopes[len(f.envelopes)-1], true
}

// testState builds a broadcastable match.state payload.
func testState(matchID string) protocol.MatchState {
	return protocol.MatchState{
		MatchID:    matchID,
		Phenomenon: protocol.ActivePhenomenon{Value: protocol.PhenomenonNone{}},
	}
}

func mustAdd(t *testing.T, match *Match, playerID string, sink Sink) int {
	t.Helper()
	slot, err := match.add(playerID, playerID, sink)
	if err != nil {
		t.Fatalf("add %s: %v", playerID, err)
	}
	return slot
}

func TestAddAssignsLowestFreeSlot(t *testing.T) {
	match := newMatch("AB12", 4)
	for slot := 0; slot < 4; slot++ {
		if got := mustAdd(t, match, fmt.Sprintf("p_%d", slot), &fakeSink{}); got != slot {
			t.Fatalf("expected slot %d, got %d", slot, got)
		}
	}

	if _, err := match.add("p_1", "p_1", &fakeSink{}); protocol.CodeOf(err) != protocol.CodeAlreadyInMatch {
		t.Fatalf("expected already_in_match, got %v", err)
	}
	if _, err := match.add("p_4", "p_4", &fakeSink{}); protocol.CodeOf(err) != protocol.CodeMatchFull {
		t.Fatalf("expected match_full, got %v", err)
	}
	if match.HostID() != "p_0" {
		t.Fatalf("expected first member as host, got %q", match.HostID())
	}
}

func TestRemoveFreesSlotForNextMember(t *testing.T) {
	match := newMatch("AB12", 4)
	for _, playerID := range []string{"p_0", "p_1", "p_2"} {
		mustAdd(t, match, playerID, &fakeSink{})
	}

	if removed, empty := match.remove("p_1"); !removed || empty {
		t.Fatalf("remove p_1 = %v, %v; want true, false", removed, empty)
	}
	if slot := mustAdd(t, match, "p_3", &fakeSink{}); slot != 1 {
		t.Fatalf("expected freed slot 1, got %d", slot)
	}

	members := match.Members()
	want := []string{"p_0", "p_3", "p_2"}
	if len(members) != len(want) {
		t.Fatalf("expected %d members, got %d", len(want), len(members))
	}
	for i, member := range members {
		if member.PlayerID != want[i] || member.Slot != i {
			t.Fatalf("member %d = %+v, want id %s slot %d", i, member, want[i], i)
		}
	}
}

func TestHostMigration(t *testing.T) {
	match := newMatch("AB12", 4)
	for _, playerID := range []string{"ana", "bob", "cid"} {
		mustAdd(t, match, playerID, &fakeSink{})
	}

	if removed, empty := match.remove("ana"); !removed || empty {
		t.Fatalf("remove ana = %v, %v; want true, false", removed, empty)
	}
	if match.HostID() != "bob" {
		t.Fatalf("expected bob promoted, got %q", match.HostID())
	}

	// A later member refills slot 0 without stealing the host.
	if slot := mustAdd(t, match, "dan", &fakeSink{}); slot != 0 {
		t.Fatalf("expected slot 0 for dan, got %d", slot)
	}
	if match.HostID() != "bob" {
		t.Fatalf("new member stole the host, got %q", match.HostID())
	}

	if removed, empty := match.remove("bob"); !removed || empty {
		t.Fatalf("remove bob = %v, %v; want true, false", removed, empty)
	}
	if match.HostID() != "dan" {
		t.Fatalf("expected dan promoted, got %q", match.HostID())
	}
	if removed, empty := match.remove("dan"); !removed || empty {
		t.Fatalf("remove dan = %v, %v; want true, false", removed, empty)
	}
	if match.HostID() != "cid" {
		t.Fatalf("expected cid promoted, got %q", match.HostID())
	}
	if removed, empty := match.remove("cid"); !removed || !empty {
		t.Fatalf("remove cid = %v, %v; want true, true", removed, empty)
	}
	if match.HostID() != "" {
		t.Fatalf("expected empty host, got %q", match.HostID())
	}

	if slot := mustAdd(t, match, "eve", &fakeSink{}); slot != 0 || match.HostID() != "eve" {
		t.Fatalf("add to empty match = slot %d, host %q", slot, match.HostID())
	}
}

func TestBroadcastReachesEveryMember(t *testing.T) {
	match := newMatch("AB12", 4)
	sinks := []*fakeSink{{}, {}}
	mustAdd(t, match, "p_1", sinks[0])
	mustAdd(t, match, "p_2", sinks[1])

	if err := match.Broadcast(protocol.TypeMatchState, testState(match.ID())); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	for i, sink := range sinks {
		envelope, ok := sink.last()
		if !ok {
			t.Fatalf("sink %d received no event", i)
		}
		if envelope.Type != protocol.TypeMatchState {
			t.Fatalf("sink %d received %q, want match.state", i, envelope.Type)
		}
		if envelope.ID != "" {
			t.Fatalf("sink %d received an event with id %q", i, envelope.ID)
		}
		var state protocol.MatchState
		if err := json.Unmarshal(envelope.Payload, &state); err != nil {
			t.Fatalf("sink %d payload: %v", i, err)
		}
		if state.MatchID != match.ID() {
			t.Fatalf("sink %d received match %q, want %q", i, state.MatchID, match.ID())
		}
	}
}

func TestBroadcastSkipsDepartedMembers(t *testing.T) {
	match := newMatch("AB12", 4)
	first, second := &fakeSink{}, &fakeSink{}
	mustAdd(t, match, "p_1", first)
	mustAdd(t, match, "p_2", second)

	if removed, _ := match.remove("p_2"); !removed {
		t.Fatal("remove p_2 reported missing")
	}
	if err := match.Broadcast(protocol.TypeMatchState, testState(match.ID())); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if first.count() != 1 {
		t.Fatalf("remaining member received %d events, want 1", first.count())
	}
	if second.count() != 0 {
		t.Fatalf("departed member received %d events, want 0", second.count())
	}

	match.remove("p_1")
	if err := match.Broadcast(protocol.TypeMatchState, testState(match.ID())); err != nil {
		t.Fatalf("broadcast empty match: %v", err)
	}
	if first.count() != 1 {
		t.Fatalf("empty match delivered %d extra events", first.count()-1)
	}
}

func TestMembersReturnsCopy(t *testing.T) {
	match := newMatch("AB12", 4)
	mustAdd(t, match, "p_1", &fakeSink{})

	members := match.Members()
	members[0].PlayerID = "mutated"
	if got := match.Members()[0].PlayerID; got != "p_1" {
		t.Fatalf("members slice aliases internal state, got %q", got)
	}
}

func TestConcurrentMatchMembership(t *testing.T) {
	match := newMatch("AB12", 4)
	const players = 4

	var wait sync.WaitGroup
	for i := 0; i < players; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			playerID := fmt.Sprintf("p_%d", i)
			if _, err := match.add(playerID, playerID, &fakeSink{}); err != nil {
				t.Errorf("add %s: %v", playerID, err)
			}
		}(i)
	}
	wait.Wait()

	seen := make(map[int]bool, players)
	for _, member := range match.Members() {
		if seen[member.Slot] {
			t.Errorf("duplicate slot %d", member.Slot)
		}
		seen[member.Slot] = true
	}
	if len(seen) != players {
		t.Fatalf("expected %d occupied slots, got %d", players, len(seen))
	}

	for i := 0; i < players; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			playerID := fmt.Sprintf("p_%d", i)
			if removed, _ := match.remove(playerID); !removed {
				t.Errorf("remove %s: not found", playerID)
			}
		}(i)
	}
	wait.Wait()

	if members := match.Members(); len(members) != 0 {
		t.Fatalf("expected empty match, got %d members", len(members))
	}
	if match.HostID() != "" {
		t.Fatalf("expected empty host, got %q", match.HostID())
	}
}
