package server

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the runtime configuration for the server. Every value can be
// overridden with an environment variable so the same image works locally and
// in production.
type Config struct {
	Addr            string
	WSPath          string
	AllowedOrigins  []string
	MaxMessageBytes int64
	ShutdownTimeout time.Duration
}

func ConfigFromEnv() Config {
	wsPath := getenv("WS_PATH", "/ws")
	if !strings.HasPrefix(wsPath, "/") {
		wsPath = "/" + wsPath
	}

	return Config{
		Addr:            ":" + getenv("PORT", "8080"),
		WSPath:          wsPath,
		AllowedOrigins:  splitList(getenv("ALLOWED_ORIGINS", "*")),
		MaxMessageBytes: getenvInt64("MAX_MESSAGE_BYTES", 32*1024),
		ShutdownTimeout: time.Duration(getenvInt64("SHUTDOWN_TIMEOUT_SECONDS", 10)) * time.Second,
	}
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
