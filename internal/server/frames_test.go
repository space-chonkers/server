package server

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/space-chonkers/server/internal/protocol"
)

type frameRecordingSink struct {
	mu     sync.Mutex
	frames []protocol.MatchFrame
}

func (s *frameRecordingSink) Send(envelope protocol.Envelope) {
	if envelope.Type != protocol.TypeMatchFrame {
		return
	}
	var frame protocol.MatchFrame
	if err := json.Unmarshal(envelope.Payload, &frame); err != nil {
		return
	}
	s.mu.Lock()
	s.frames = append(s.frames, frame)
	s.mu.Unlock()
}

func (s *frameRecordingSink) frameCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames)
}

func (s *frameRecordingSink) lastFrame() (protocol.MatchFrame, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 0 {
		return protocol.MatchFrame{}, false
	}
	return s.frames[len(s.frames)-1], true
}

func TestPlayingMatchEmitsFramesWithoutInput(t *testing.T) {
	s := newTestServerWithManager(t, testConfig(), testMatchManager())
	sink := &frameRecordingSink{}
	if _, err := s.matches.Create("create", "p_1", "ana", sink, 0); err != nil {
		t.Fatalf("create match: %v", err)
	}
	if err := s.matches.Start("p_1", time.Now().UnixMilli()); err != nil {
		t.Fatalf("start match: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for sink.frameCount() < 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.frameCount(); got < 5 {
		t.Fatalf("emitted %d frames without input in one second, want at least 5", got)
	}
	frame, _ := sink.lastFrame()
	if len(frame.Players) != 1 || frame.Players[0].ID != "p_1" {
		t.Fatalf("frame players = %+v, want p_1 before any input", frame.Players)
	}
}

func TestStopFrameLoopStopsBroadcast(t *testing.T) {
	s := newTestServerWithManager(t, testConfig(), testMatchManager())
	sink := &frameRecordingSink{}
	if _, err := s.matches.Create("create", "p_1", "ana", sink, 0); err != nil {
		t.Fatalf("create match: %v", err)
	}
	if err := s.matches.Start("p_1", time.Now().UnixMilli()); err != nil {
		t.Fatalf("start match: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for sink.frameCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.frameCount(); got == 0 {
		t.Fatal("playing match emitted no frames")
	}

	s.stopFrameLoop()
	before := sink.frameCount()
	time.Sleep(100 * time.Millisecond)
	if got := sink.frameCount(); got != before {
		t.Fatalf("emitted %d frames after stop, want 0", got-before)
	}
}
