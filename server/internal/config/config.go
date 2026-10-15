// Package config loads the server configuration from the environment.
//
// Every value comes from an environment variable. There is no configuration
// file and no default pointing at a real system: a missing required value
// stops the server at startup rather than silently falling back to whatever
// happens to work on one machine.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

const envPrefix = "CHOREO_"

// Config is the whole configuration of the server.
type Config struct {
	// Env names the deployment: dev, test or prod. It changes how much is
	// logged, never what the server does.
	Env string

	// HTTPAddr is the listen address, as host:port.
	HTTPAddr string

	// DatabaseURL is a PostgreSQL connection string.
	DatabaseURL string

	// LogLevel filters structured logs.
	LogLevel slog.Level

	// ShutdownTimeout bounds how long in-flight requests may finish after a
	// termination signal.
	ShutdownTimeout time.Duration
}

// Load reads the configuration from the environment.
//
// It reports every problem it finds at once: fixing configuration one error
// per restart is miserable, and it is the first thing anyone does on a new
// machine.
func Load() (Config, error) {
	var problems []string

	cfg := Config{
		Env:             lookup("ENV", "dev"),
		HTTPAddr:        lookup("HTTP_ADDR", ":8080"),
		DatabaseURL:     lookup("DATABASE_URL", ""),
		ShutdownTimeout: 15 * time.Second,
	}

	if cfg.DatabaseURL == "" {
		problems = append(problems, envPrefix+"DATABASE_URL is required")
	}

	switch cfg.Env {
	case "dev", "test", "prod":
	default:
		problems = append(problems,
			fmt.Sprintf("%sENV must be dev, test or prod, got %q", envPrefix, cfg.Env))
	}

	level, err := parseLevel(lookup("LOG_LEVEL", "info"))
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.LogLevel = level

	if raw := lookup("SHUTDOWN_TIMEOUT", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		switch {
		case err != nil:
			problems = append(problems,
				fmt.Sprintf("%sSHUTDOWN_TIMEOUT is not a duration: %v", envPrefix, err))
		case d <= 0:
			problems = append(problems, envPrefix+"SHUTDOWN_TIMEOUT must be positive")
		default:
			cfg.ShutdownTimeout = d
		}
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("invalid configuration:\n  - %s",
			strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}

// Redacted returns the configuration with the credentials taken out, safe to
// log at startup. The database URL carries a password.
func (c Config) Redacted() map[string]any {
	return map[string]any{
		"env":              c.Env,
		"http_addr":        c.HTTPAddr,
		"log_level":        c.LogLevel.String(),
		"shutdown_timeout": c.ShutdownTimeout.String(),
		"database":         redactURL(c.DatabaseURL),
	}
}

// redactURL keeps only what helps diagnose a connection problem: which host,
// which database. Never the password.
func redactURL(raw string) string {
	at := strings.LastIndex(raw, "@")
	if at < 0 {
		return "(set)"
	}
	return "//***@" + raw[at+1:]
}

func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf(
			"%sLOG_LEVEL must be debug, info, warn or error, got %q", envPrefix, raw)
	}
}

func lookup(name, fallback string) string {
	if v, ok := os.LookupEnv(envPrefix + name); ok && v != "" {
		return v
	}
	return fallback
}
