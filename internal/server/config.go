package server

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

// Config is the runtime configuration for the server. Transport values can be
// overridden with an environment variable so the same image works locally and
// in production; gameplay values come from DefaultConfig.
type Config struct {
	Addr               string
	WSPath             string
	AllowedOrigins     []string
	MaxMessageBytes    int64
	ShutdownTimeout    time.Duration
	TickRate           float64
	FrameRate          float64
	MaxPlayersPerMatch int
	MaxMatches         int
	Rules              protocol.Rules
}

// DefaultConfig returns the server defaults, including gameplay limits.
func DefaultConfig() Config {
	return Config{
		Addr:               ":8080",
		WSPath:             "/ws",
		AllowedOrigins:     []string{"*"},
		MaxMessageBytes:    32 * 1024,
		ShutdownTimeout:    10 * time.Second,
		TickRate:           30,
		FrameRate:          15,
		MaxPlayersPerMatch: 4,
		MaxMatches:         64,
		Rules: protocol.Rules{
			Grid:                  protocol.GridSize{Width: 50, Height: 50},
			InvestigationCount:    10,
			BuildCost:             protocol.BuildCost{Water: 5, Oxygen: 5, Energy: 5, Food: 0},
			PlayerSpeed:           4,
			PhenomenonRollSeconds: 30,
		},
	}
}

// Welcome returns the first server event for a connection.
func (cfg Config) Welcome(playerID string) protocol.Welcome {
	return protocol.Welcome{
		PlayerID:        playerID,
		ProtocolVersion: protocol.ProtocolVersion,
		ServerTimeMs:    time.Now().UnixMilli(),
		TickRate:        cfg.TickRate,
		FrameRate:       cfg.FrameRate,
		Limits: protocol.Limits{
			MaxMessageBytes:    int(cfg.MaxMessageBytes),
			MaxPlayersPerMatch: cfg.MaxPlayersPerMatch,
			MaxNicknameLength:  protocol.MaxNicknameLength,
			MaxMatches:         cfg.MaxMatches,
		},
		Rules: cfg.Rules,
	}
}

// ConfigFromEnv loads the documented environment overrides and validates the
// result. Invalid values fail startup instead of silently falling back.
func ConfigFromEnv() (Config, error) {
	cfg := DefaultConfig()

	if value, ok := envValue("PORT"); ok {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("invalid PORT %q: want an integer between 1 and 65535", value)
		}
		cfg.Addr = ":" + strconv.Itoa(port)
	}

	if value, ok := envValue("WS_PATH"); ok {
		if !strings.HasPrefix(value, "/") {
			value = "/" + value
		}
		cfg.WSPath = value
	}

	if value, ok := envValue("ALLOWED_ORIGINS"); ok {
		origins := splitList(value)
		if len(origins) == 0 {
			return Config{}, fmt.Errorf("invalid ALLOWED_ORIGINS %q: want a comma-separated list", value)
		}
		cfg.AllowedOrigins = origins
	}

	if value, ok := envValue("MAX_MESSAGE_BYTES"); ok {
		size, err := strconv.ParseInt(value, 10, 64)
		if err != nil || size <= 0 {
			return Config{}, fmt.Errorf("invalid MAX_MESSAGE_BYTES %q: want a positive integer", value)
		}
		cfg.MaxMessageBytes = size
	}

	if value, ok := envValue("SHUTDOWN_TIMEOUT_SECONDS"); ok {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || seconds <= 0 ||
			seconds > int64((1<<63-1)/int64(time.Second)) {
			return Config{}, fmt.Errorf("invalid SHUTDOWN_TIMEOUT_SECONDS %q: want a positive integer", value)
		}
		cfg.ShutdownTimeout = time.Duration(seconds) * time.Second
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks the values that can make the process or a match fail after
// startup. It is called on environment-derived configuration.
func (cfg Config) Validate() error {
	if err := cfg.validateTransport(); err != nil {
		return err
	}
	return cfg.validateRules()
}

func (cfg Config) validateTransport() error {
	if cfg.WSPath == "" || cfg.WSPath == "/" || !strings.HasPrefix(cfg.WSPath, "/") {
		return fmt.Errorf("invalid WS_PATH %q: want a path such as /ws", cfg.WSPath)
	}
	if strings.HasSuffix(cfg.WSPath, "/") ||
		strings.ContainsAny(cfg.WSPath, " \t\r\n?#") {
		return fmt.Errorf("invalid WS_PATH %q: want an exact path without a trailing slash, whitespace, or query", cfg.WSPath)
	}

	if len(cfg.AllowedOrigins) == 0 {
		return fmt.Errorf("invalid ALLOWED_ORIGINS: want at least one origin or *")
	}
	for _, origin := range cfg.AllowedOrigins {
		if err := validateOrigin(origin); err != nil {
			return err
		}
	}

	if cfg.MaxMessageBytes <= 0 {
		return fmt.Errorf("invalid MAX_MESSAGE_BYTES %d: want a positive integer", cfg.MaxMessageBytes)
	}
	if cfg.ShutdownTimeout <= 0 {
		return fmt.Errorf("invalid SHUTDOWN_TIMEOUT_SECONDS: want a positive duration")
	}
	if !finitePositive(cfg.TickRate) {
		return fmt.Errorf("invalid TICK_RATE %v: want a finite positive number", cfg.TickRate)
	}
	if !finitePositive(cfg.FrameRate) {
		return fmt.Errorf("invalid FRAME_RATE %v: want a finite positive number", cfg.FrameRate)
	}
	if cfg.FrameRate > cfg.TickRate {
		return fmt.Errorf("invalid FRAME_RATE %v: must not exceed TICK_RATE %v", cfg.FrameRate, cfg.TickRate)
	}
	if cfg.MaxPlayersPerMatch < 1 || cfg.MaxPlayersPerMatch > 4 {
		return fmt.Errorf("invalid MAX_PLAYERS_PER_MATCH %d: want 1..4", cfg.MaxPlayersPerMatch)
	}
	if cfg.MaxMatches <= 0 {
		return fmt.Errorf("invalid MAX_MATCHES %d: want a positive integer", cfg.MaxMatches)
	}
	return nil
}

func (cfg Config) validateRules() error {
	if cfg.Rules.Grid.Width <= 0 || cfg.Rules.Grid.Height <= 0 {
		return fmt.Errorf("invalid grid %dx%d: want positive dimensions",
			cfg.Rules.Grid.Width, cfg.Rules.Grid.Height)
	}
	available := cfg.Rules.Grid.Width*cfg.Rules.Grid.Height - 9
	if available < 1 {
		return fmt.Errorf("invalid grid %dx%d: need at least a 3x3 base",
			cfg.Rules.Grid.Width, cfg.Rules.Grid.Height)
	}
	if cfg.Rules.InvestigationCount < 1 || cfg.Rules.InvestigationCount > available {
		return fmt.Errorf("invalid investigation count %d: want 1..%d",
			cfg.Rules.InvestigationCount, available)
	}

	for _, cost := range []struct {
		name  string
		value float64
	}{
		{"water", cfg.Rules.BuildCost.Water},
		{"oxygen", cfg.Rules.BuildCost.Oxygen},
		{"energy", cfg.Rules.BuildCost.Energy},
		{"food", cfg.Rules.BuildCost.Food},
	} {
		if math.IsNaN(cost.value) || math.IsInf(cost.value, 0) || cost.value < 0 {
			return fmt.Errorf("invalid build cost %s %v: want a finite non-negative number",
				cost.name, cost.value)
		}
	}
	if !finitePositive(cfg.Rules.PlayerSpeed) {
		return fmt.Errorf("invalid player speed %v: want a finite positive number", cfg.Rules.PlayerSpeed)
	}
	if !finitePositive(cfg.Rules.PhenomenonRollSeconds) {
		return fmt.Errorf("invalid phenomenon roll interval %v: want a finite positive number",
			cfg.Rules.PhenomenonRollSeconds)
	}
	if err := game.ValidatePhenomenonConfig(matchOptions(cfg).GameConfig); err != nil {
		return fmt.Errorf("invalid phenomenon config: %w", err)
	}
	return nil
}

func envValue(key string) (string, bool) {
	value := strings.TrimSpace(os.Getenv(key))
	return value, value != ""
}

func validateOrigin(origin string) error {
	if origin == "*" {
		return nil
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid ALLOWED_ORIGINS entry %q: want * or http(s)://host[:port]", origin)
	}
	return nil
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
