// Package config loads InferLab's runtime configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"time"
)

// Environment variable names.
const (
	EnvAddr            = "INFERLAB_ADDR"
	EnvShutdownTimeout = "INFERLAB_SHUTDOWN_TIMEOUT"
	EnvLogLevel        = "INFERLAB_LOG_LEVEL"
)

// Defaults applied when a variable is unset or empty.
const (
	DefaultAddr            = ":8080"
	DefaultShutdownTimeout = 10 * time.Second
)

// Config is the process-wide configuration.
type Config struct {
	// Addr is the TCP address the HTTP server listens on.
	Addr string
	// ShutdownTimeout bounds how long graceful shutdown may take.
	ShutdownTimeout time.Duration
	// LogLevel is the minimum level that is logged.
	LogLevel slog.Level
}

// Load builds a Config using getenv to read variables (typically os.Getenv).
// Taking the lookup as a parameter keeps Load free of global state.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:            DefaultAddr,
		ShutdownTimeout: DefaultShutdownTimeout,
		LogLevel:        slog.LevelInfo,
	}

	if v := getenv(EnvAddr); v != "" {
		cfg.Addr = v
	}

	if v := getenv(EnvShutdownTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", EnvShutdownTimeout, err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("%s: must be positive, got %s", EnvShutdownTimeout, v)
		}
		cfg.ShutdownTimeout = d
	}

	if v := getenv(EnvLogLevel); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return Config{}, fmt.Errorf("%s: %w", EnvLogLevel, err)
		}
	}

	return cfg, nil
}
