package server

import (
	"reflect"
	"testing"
	"time"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT",
		"WS_PATH",
		"ALLOWED_ORIGINS",
		"MAX_MESSAGE_BYTES",
		"SHUTDOWN_TIMEOUT_SECONDS",
	} {
		t.Setenv(key, "")
	}
}

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
	if welcome.TickRate != 60 || welcome.FrameRate != 60 {
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

func TestConfigFromEnvDefaults(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Fatalf("env defaults = %+v, want %+v", cfg, DefaultConfig())
	}
}

func TestConfigFromEnvOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("WS_PATH", "game")
	t.Setenv("ALLOWED_ORIGINS", "https://game.example.com, http://localhost:3000")
	t.Setenv("MAX_MESSAGE_BYTES", "1024")
	t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", "3")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("addr = %q, want :9090", cfg.Addr)
	}
	if cfg.WSPath != "/game" {
		t.Fatalf("ws path = %q, want /game", cfg.WSPath)
	}
	if len(cfg.AllowedOrigins) != 2 ||
		cfg.AllowedOrigins[0] != "https://game.example.com" ||
		cfg.AllowedOrigins[1] != "http://localhost:3000" {
		t.Fatalf("origins = %+v", cfg.AllowedOrigins)
	}
	if cfg.MaxMessageBytes != 1024 {
		t.Fatalf("max message bytes = %d", cfg.MaxMessageBytes)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Fatalf("shutdown timeout = %v", cfg.ShutdownTimeout)
	}
}

func TestConfigFromEnvRejectsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "port text", key: "PORT", value: "abc"},
		{name: "port zero", key: "PORT", value: "0"},
		{name: "port high", key: "PORT", value: "70000"},
		{name: "message text", key: "MAX_MESSAGE_BYTES", value: "abc"},
		{name: "message zero", key: "MAX_MESSAGE_BYTES", value: "0"},
		{name: "message negative", key: "MAX_MESSAGE_BYTES", value: "-1"},
		{name: "shutdown text", key: "SHUTDOWN_TIMEOUT_SECONDS", value: "abc"},
		{name: "shutdown zero", key: "SHUTDOWN_TIMEOUT_SECONDS", value: "0"},
		{name: "shutdown negative", key: "SHUTDOWN_TIMEOUT_SECONDS", value: "-1"},
		{name: "ws root", key: "WS_PATH", value: "/"},
		{name: "ws trailing slash", key: "WS_PATH", value: "/ws/"},
		{name: "ws query", key: "WS_PATH", value: "/ws?x=1"},
		{name: "origin malformed", key: "ALLOWED_ORIGINS", value: "not an origin"},
		{name: "origin path", key: "ALLOWED_ORIGINS", value: "https://game.example.com/path"},
		{name: "origin empty list", key: "ALLOWED_ORIGINS", value: ","},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(test.key, test.value)
			if _, err := ConfigFromEnv(); err == nil {
				t.Fatalf("ConfigFromEnv accepted %s=%q", test.key, test.value)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("default config is invalid: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "ws empty", mutate: func(cfg *Config) { cfg.WSPath = "" }},
		{name: "ws root", mutate: func(cfg *Config) { cfg.WSPath = "/" }},
		{name: "ws trailing slash", mutate: func(cfg *Config) { cfg.WSPath = "/ws/" }},
		{name: "origins empty", mutate: func(cfg *Config) { cfg.AllowedOrigins = nil }},
		{name: "message zero", mutate: func(cfg *Config) { cfg.MaxMessageBytes = 0 }},
		{name: "shutdown zero", mutate: func(cfg *Config) { cfg.ShutdownTimeout = 0 }},
		{name: "tick zero", mutate: func(cfg *Config) { cfg.TickRate = 0 }},
		{name: "frame zero", mutate: func(cfg *Config) { cfg.FrameRate = 0 }},
		{name: "frame over tick", mutate: func(cfg *Config) { cfg.FrameRate = 61 }},
		{name: "players zero", mutate: func(cfg *Config) { cfg.MaxPlayersPerMatch = 0 }},
		{name: "players high", mutate: func(cfg *Config) { cfg.MaxPlayersPerMatch = 5 }},
		{name: "matches zero", mutate: func(cfg *Config) { cfg.MaxMatches = 0 }},
		{name: "grid zero", mutate: func(cfg *Config) { cfg.Rules.Grid.Width = 0 }},
		{name: "grid too small", mutate: func(cfg *Config) { cfg.Rules.Grid = protocol.GridSize{Width: 2, Height: 2} }},
		{name: "investigations zero", mutate: func(cfg *Config) { cfg.Rules.InvestigationCount = 0 }},
		{name: "investigations high", mutate: func(cfg *Config) { cfg.Rules.InvestigationCount = 2492 }},
		{name: "build cost negative", mutate: func(cfg *Config) { cfg.Rules.BuildCost.Water = -1 }},
		{name: "speed zero", mutate: func(cfg *Config) { cfg.Rules.PlayerSpeed = 0 }},
		{name: "roll zero", mutate: func(cfg *Config) { cfg.Rules.PhenomenonRollSeconds = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate accepted invalid config")
			}
		})
	}
}

func TestMatchOptionsUseConfiguredRules(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules.Grid = protocol.GridSize{Width: 60, Height: 40}
	cfg.Rules.InvestigationCount = 7
	cfg.Rules.BuildCost = protocol.BuildCost{Water: 1, Oxygen: 2, Energy: 3, Food: 4}
	cfg.Rules.PhenomenonRollSeconds = 45

	options := matchOptions(cfg)
	if options.Grid != cfg.Rules.Grid {
		t.Fatalf("grid = %+v, want %+v", options.Grid, cfg.Rules.Grid)
	}
	if options.GameConfig.InvestigationCount != 7 {
		t.Fatalf("investigation count = %d", options.GameConfig.InvestigationCount)
	}
	if options.GameConfig.BuildCost != (game.BuildCost{Water: 1, Oxygen: 2, Energy: 3}) {
		t.Fatalf("build cost = %+v", options.GameConfig.BuildCost)
	}
	if options.GameConfig.Phenomena.RollSeconds != 45 {
		t.Fatalf("phenomenon roll seconds = %v, want the published 45", options.GameConfig.Phenomena.RollSeconds)
	}
}

func TestWelcomeRulesMatchGameDefaults(t *testing.T) {
	welcome := DefaultConfig().Welcome("p_test")
	defaults := game.DefaultConfig()

	if welcome.Rules.InvestigationCount != defaults.InvestigationCount {
		t.Fatalf("investigation count = %d, want %d",
			welcome.Rules.InvestigationCount, defaults.InvestigationCount)
	}
	if welcome.Rules.BuildCost != (protocol.BuildCost{
		Water:  defaults.BuildCost.Water,
		Oxygen: defaults.BuildCost.Oxygen,
		Energy: defaults.BuildCost.Energy,
	}) {
		t.Fatalf("build cost = %+v, want game defaults %+v",
			welcome.Rules.BuildCost, defaults.BuildCost)
	}
	if welcome.Rules.PhenomenonRollSeconds != defaults.Phenomena.RollSeconds {
		t.Fatalf("phenomenon roll seconds = %v, want %v",
			welcome.Rules.PhenomenonRollSeconds, defaults.Phenomena.RollSeconds)
	}
}
