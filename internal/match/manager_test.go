package match

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/space-chonkers/server/internal/protocol"
)

func mustCreate(t *testing.T, manager *Manager, playerID string, sink Sink) *Match {
	t.Helper()
	match, err := manager.Create("create", playerID, playerID, sink, 0)
	if err != nil {
		t.Fatalf("create %s: %v", playerID, err)
	}
	return match
}

func mustJoin(t *testing.T, manager *Manager, matchID, playerID string, sink Sink) *Match {
	t.Helper()
	match, err := manager.Join("join", matchID, playerID, playerID, sink, 0)
	if err != nil {
		t.Fatalf("join %s: %v", playerID, err)
	}
	return match
}

func mustPublish(t *testing.T, match *Match) {
	t.Helper()
	if err := match.publishSnapshot(0); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestCreateAssignsUniqueIDs(t *testing.T) {
	manager := NewManager(Options{MaxMatches: 16, MaxPlayersPerMatch: 4})
	seen := make(map[string]bool, 16)

	for i := 0; i < 16; i++ {
		match := mustCreate(t, manager, fmt.Sprintf("p_%d", i), &fakeSink{})
		if len(match.ID()) != idLength {
			t.Fatalf("expected %d-char id, got %q", idLength, match.ID())
		}
		if seen[match.ID()] {
			t.Fatalf("duplicate match id %q", match.ID())
		}
		seen[match.ID()] = true
		if got, ok := manager.Get(match.ID()); !ok || got != match {
			t.Fatalf("get %q = %v, %v", match.ID(), got, ok)
		}
	}

	if manager.Count() != 16 {
		t.Fatalf("expected 16 matches, got %d", manager.Count())
	}
	if _, err := manager.Create("create", "p_extra", "p_extra", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeServerFull {
		t.Fatalf("expected server_full, got %v", err)
	}
}

func TestCreateRejectsPlayerAlreadyInMatch(t *testing.T) {
	manager := NewManager(Options{MaxMatches: 4, MaxPlayersPerMatch: 4})
	match := mustCreate(t, manager, "p_1", &fakeSink{})

	if _, err := manager.Create("create", "p_1", "p_1", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeAlreadyInMatch {
		t.Fatalf("create: expected already_in_match, got %v", err)
	}
	if _, err := manager.Join("join", match.ID(), "p_1", "p_1", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeAlreadyInMatch {
		t.Fatalf("join: expected already_in_match, got %v", err)
	}
	if _, err := manager.JoinAny("join", "p_1", "p_1", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeAlreadyInMatch {
		t.Fatalf("join any: expected already_in_match, got %v", err)
	}
}

func TestJoinAndLeave(t *testing.T) {
	manager := NewManager(Options{MaxMatches: 4, MaxPlayersPerMatch: 2})
	match := mustCreate(t, manager, "p_1", &fakeSink{})
	if match.HostID() != "p_1" {
		t.Fatalf("expected creator as host, got %q", match.HostID())
	}

	if joined := mustJoin(t, manager, match.ID(), "p_2", &fakeSink{}); joined != match {
		t.Fatal("join returned a different match")
	}
	if _, err := manager.Join("join", match.ID(), "p_3", "p_3", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeMatchFull {
		t.Fatalf("expected match_full, got %v", err)
	}
	if _, err := manager.Join("join", "NOPE", "p_3", "p_3", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeMatchNotFound {
		t.Fatalf("expected match_not_found, got %v", err)
	}
	if err := manager.Leave("p_9", 0); protocol.CodeOf(err) != protocol.CodeNotInMatch {
		t.Fatalf("expected not_in_match, got %v", err)
	}

	if err := manager.Leave("p_2", 0); err != nil {
		t.Fatalf("leave p_2: %v", err)
	}
	if err := manager.Leave("p_2", 0); protocol.CodeOf(err) != protocol.CodeNotInMatch {
		t.Fatalf("expected not_in_match, got %v", err)
	}
	if err := manager.Leave("p_1", 0); err != nil {
		t.Fatalf("leave p_1: %v", err)
	}
	if _, ok := manager.Get(match.ID()); ok {
		t.Fatal("empty match still registered")
	}
	if manager.Count() != 0 {
		t.Fatalf("expected empty registry, got %d", manager.Count())
	}
}

func TestJoinAnyFillsThenCreates(t *testing.T) {
	manager := NewManager(Options{MaxMatches: 2, MaxPlayersPerMatch: 2})
	first, err := manager.JoinAny("join", "p_1", "p_1", &fakeSink{}, 0)
	if err != nil {
		t.Fatalf("join any p_1: %v", err)
	}
	second, err := manager.JoinAny("join", "p_2", "p_2", &fakeSink{}, 0)
	if err != nil {
		t.Fatalf("join any p_2: %v", err)
	}
	if second != first {
		t.Fatal("expected p_2 to fill the first lobby")
	}

	third, err := manager.JoinAny("join", "p_3", "p_3", &fakeSink{}, 0)
	if err != nil {
		t.Fatalf("join any p_3: %v", err)
	}
	if third == first {
		t.Fatal("expected p_3 to start a second lobby")
	}
	if manager.Count() != 2 {
		t.Fatalf("expected 2 matches, got %d", manager.Count())
	}

	fourth, err := manager.JoinAny("join", "p_4", "p_4", &fakeSink{}, 0)
	if err != nil {
		t.Fatalf("join any p_4: %v", err)
	}
	if fourth != third {
		t.Fatal("expected p_4 to fill the second lobby")
	}
	if _, err := manager.JoinAny("join", "p_5", "p_5", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeServerFull {
		t.Fatalf("expected server_full, got %v", err)
	}
}

func TestCloseIsTerminal(t *testing.T) {
	manager := NewManager(Options{MaxMatches: 4, MaxPlayersPerMatch: 4})
	mustCreate(t, manager, "p_1", &fakeSink{})

	manager.Close()
	if manager.Count() != 0 {
		t.Fatalf("expected empty registry, got %d", manager.Count())
	}
	if _, err := manager.Create("create", "p_2", "p_2", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeServerFull {
		t.Fatalf("create after close: expected server_full, got %v", err)
	}
	if _, err := manager.JoinAny("join", "p_2", "p_2", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeServerFull {
		t.Fatalf("join any after close: expected server_full, got %v", err)
	}
	if _, err := manager.Join("join", "AB12", "p_2", "p_2", &fakeSink{}, 0); protocol.CodeOf(err) != protocol.CodeServerFull {
		t.Fatalf("join after close: expected server_full, got %v", err)
	}
	manager.Close()
}

func TestDeleteRemovesMatch(t *testing.T) {
	manager := NewManager(Options{MaxMatches: 4, MaxPlayersPerMatch: 4})
	match := mustCreate(t, manager, "p_1", &fakeSink{})

	if !manager.Delete(match.ID()) {
		t.Fatal("delete reported missing match")
	}
	if _, ok := manager.Get(match.ID()); ok {
		t.Fatal("match still registered after delete")
	}
	if manager.Count() != 0 {
		t.Fatalf("expected empty registry, got %d", manager.Count())
	}
	if manager.Delete(match.ID()) {
		t.Fatal("second delete reported success")
	}
}

func TestTwoMatchesAreIsolated(t *testing.T) {
	manager := NewManager(Options{MaxMatches: 2, MaxPlayersPerMatch: 2})
	firstA, firstB, secondA := &fakeSink{}, &fakeSink{}, &fakeSink{}
	first := mustCreate(t, manager, "p_a1", firstA)
	second := mustCreate(t, manager, "p_b1", secondA)
	mustJoin(t, manager, first.ID(), "p_a2", firstB)

	firstABefore, firstBBefore := firstA.count(), firstB.count()
	secondABefore := secondA.count()
	mustPublish(t, first)
	if firstA.count() != firstABefore+1 || firstB.count() != firstBBefore+1 {
		t.Fatalf("first match delivery = %d, %d; want %d, %d",
			firstA.count(), firstB.count(), firstABefore+1, firstBBefore+1)
	}
	if secondA.count() != secondABefore {
		t.Fatalf("second match received events from the first")
	}

	mustPublish(t, second)
	if secondA.count() != secondABefore+1 {
		t.Fatalf("second match delivery = %d, want %d", secondA.count(), secondABefore+1)
	}
	if firstA.count() != firstABefore+1 || firstB.count() != firstBBefore+1 {
		t.Fatal("first match received events from the second")
	}

	for _, member := range first.Members() {
		if member.PlayerID == "p_b1" {
			t.Fatal("second match member leaked into the first")
		}
	}
	if got := len(second.Members()); got != 1 {
		t.Fatalf("second match members = %d, want 1", got)
	}

	if err := manager.Leave("p_a1", 0); err != nil {
		t.Fatalf("leave p_a1: %v", err)
	}
	if err := manager.Leave("p_a2", 0); err != nil {
		t.Fatalf("leave p_a2: %v", err)
	}
	if _, ok := manager.Get(first.ID()); ok {
		t.Fatal("empty first match still registered")
	}
	live, ok := manager.Get(second.ID())
	if !ok || len(live.Members()) != 1 {
		t.Fatal("emptying the first match disturbed the second")
	}
}

func TestConcurrentLifecycle(t *testing.T) {
	manager := NewManager(Options{MaxMatches: 16, MaxPlayersPerMatch: 4})
	const (
		workers    = 8
		iterations = 50
	)

	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for i := 0; i < iterations; i++ {
				playerID := fmt.Sprintf("p_%d_%d", worker, i)
				match, err := manager.Create("create", playerID, playerID, &fakeSink{}, 0)
				if err != nil {
					t.Errorf("worker %d create: %v", worker, err)
					return
				}
				if err := match.publishSnapshot(0); err != nil {
					t.Errorf("worker %d publish: %v", worker, err)
					return
				}
				match.Members()
				if err := manager.Leave(playerID, 0); err != nil {
					t.Errorf("worker %d leave: %v", worker, err)
					return
				}
			}
		}(worker)
	}
	wait.Wait()

	if manager.Count() != 0 {
		t.Fatalf("expected empty registry, got %d", manager.Count())
	}
}

func TestManagerDoesNotLeakGoroutines(t *testing.T) {
	baseline := runtime.NumGoroutine()
	manager := NewManager(Options{MaxMatches: 64, MaxPlayersPerMatch: 4})

	for i := 0; i < 200; i++ {
		playerID := fmt.Sprintf("p_%d", i)
		match := mustCreate(t, manager, playerID, &fakeSink{})
		mustPublish(t, match)
		if err := manager.Leave(playerID, 0); err != nil {
			t.Fatalf("leave %s: %v", playerID, err)
		}
	}

	// Match lifecycle starts no goroutines today; the bounded poll guards
	// against growth as later tickets add work.
	deadline := time.Now().Add(200 * time.Millisecond)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baseline {
		t.Fatalf("goroutines grew from %d to %d", baseline, got)
	}
}
