package server

import (
	"testing"

	"github.com/space-chonkers/server/internal/protocol"
)

func TestDefaultConfigWelcome(t *testing.T) {
	welcome := DefaultConfig().Welcome("p_test")

	if welcome.PlayerID != "p_test" {
		t.Fatalf("expected player id p_test, got %q", welcome.PlayerID)
	}
	if welcome.ProtocolVersion != protocol.ProtocolVersion {
		t.Fatalf("expected protocol version %d, got %d", protocol.ProtocolVersion, welcome.ProtocolVersion)
	}
	if welcome.ServerTimeMs <= 0 {
		t.Fatalf("expected server time, got %d", welcome.ServerTimeMs)
	}
	if welcome.TickRate != 30 || welcome.FrameRate != 15 {
		t.Fatalf("unexpected rates tick=%v frame=%v", welcome.TickRate, welcome.FrameRate)
	}
	if welcome.Limits.MaxMessageBytes != 32768 ||
		welcome.Limits.MaxPlayersPerMatch != 4 ||
		welcome.Limits.MaxNicknameLength != 16 ||
		welcome.Limits.MaxMatches != 64 {
		t.Fatalf("unexpected limits %+v", welcome.Limits)
	}
	if welcome.Rules.Grid != (protocol.GridSize{Width: 50, Height: 50}) ||
		welcome.Rules.InvestigationCount != 10 ||
		welcome.Rules.BuildCost != (protocol.BuildCost{Water: 5, Oxygen: 5, Energy: 5}) ||
		welcome.Rules.PlayerSpeed != 4 ||
		welcome.Rules.PhenomenonRollSeconds != 30 {
		t.Fatalf("unexpected rules %+v", welcome.Rules)
	}
}
