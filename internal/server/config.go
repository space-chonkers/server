package server

import (
	"os"
	"strconv"
	"strings"
	"time"

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
	// MarsGeoDataDir holds the HiRISE product files named by the embedded
	// marsgeo registry (MARSGEO_DATA_DIR). Empty: the Mars terrain endpoint
	// answers 503 data-unavailable; the rest of the server is unaffected.
	MarsGeoDataDir string
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

func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	cfg.Addr = ":" + getenv("PORT", strings.TrimPrefix(cfg.Addr, ":"))

	wsPath := getenv("WS_PATH", cfg.WSPath)
	if !strings.HasPrefix(wsPath, "/") {
		wsPath = "/" + wsPath
	}
	cfg.WSPath = wsPath

	cfg.AllowedOrigins = splitList(getenv("ALLOWED_ORIGINS", strings.Join(cfg.AllowedOrigins, ",")))
	cfg.MaxMessageBytes = getenvInt64("MAX_MESSAGE_BYTES", cfg.MaxMessageBytes)
	cfg.ShutdownTimeout = getenvDuration("SHUTDOWN_TIMEOUT_SECONDS", cfg.ShutdownTimeout)
	cfg.MarsGeoDataDir = getenv("MARSGEO_DATA_DIR", cfg.MarsGeoDataDir)

	return cfg
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getenvInt64(key string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(key)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	seconds, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(key)), 10, 64)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}
