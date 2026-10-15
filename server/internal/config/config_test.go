package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/thatwasyahya/choreo/server/internal/config"
)

// setMinimal gives the process only what Load strictly requires, so each test
// starts from the same floor.
func setMinimal(t *testing.T) {
	t.Helper()
	t.Setenv("CHOREO_DATABASE_URL", "postgres://user:hunter2@db.example:5432/choreo")
	t.Setenv("CHOREO_ENV", "")
	t.Setenv("CHOREO_HTTP_ADDR", "")
	t.Setenv("CHOREO_LOG_LEVEL", "")
	t.Setenv("CHOREO_SHUTDOWN_TIMEOUT", "")
}

func TestLoadAppliesDefaults(t *testing.T) {
	setMinimal(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() = %v, want no error", err)
	}

	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want %q", cfg.Env, "dev")
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelInfo)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 15*time.Second)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	setMinimal(t)
	t.Setenv("CHOREO_DATABASE_URL", "")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() = nil error, want a failure when the database URL is missing")
	} else if !strings.Contains(err.Error(), "CHOREO_DATABASE_URL") {
		t.Errorf("error %q does not name the missing variable", err)
	}
}

// Load must report everything wrong at once. Discovering configuration errors
// one restart at a time is the worst possible first contact with a project.
func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	setMinimal(t)
	t.Setenv("CHOREO_DATABASE_URL", "")
	t.Setenv("CHOREO_ENV", "staging")
	t.Setenv("CHOREO_LOG_LEVEL", "chatty")
	t.Setenv("CHOREO_SHUTDOWN_TIMEOUT", "soon")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil error, want a failure")
	}

	for _, want := range []string{
		"CHOREO_DATABASE_URL",
		"CHOREO_ENV",
		"CHOREO_LOG_LEVEL",
		"CHOREO_SHUTDOWN_TIMEOUT",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%v", want, err)
		}
	}
}

func TestLoadParsesLogLevels(t *testing.T) {
	for raw, want := range map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"Error":   slog.LevelError,
	} {
		t.Run(raw, func(t *testing.T) {
			setMinimal(t)
			t.Setenv("CHOREO_LOG_LEVEL", raw)

			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load() = %v, want no error", err)
			}
			if cfg.LogLevel != want {
				t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, want)
			}
		})
	}
}

func TestLoadRejectsNonPositiveShutdownTimeout(t *testing.T) {
	setMinimal(t)
	t.Setenv("CHOREO_SHUTDOWN_TIMEOUT", "0s")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() = nil error, want a failure on a zero shutdown timeout")
	}
}

// The configuration is logged at startup. The database password must not come
// along for the ride.
func TestRedactedHidesThePassword(t *testing.T) {
	setMinimal(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() = %v, want no error", err)
	}

	rendered := strings.ToLower(fmtMap(cfg.Redacted()))
	if strings.Contains(rendered, "hunter2") {
		t.Errorf("Redacted() leaks the password: %s", rendered)
	}
	if !strings.Contains(rendered, "db.example") {
		t.Errorf("Redacted() dropped the host, which is what makes it useful: %s", rendered)
	}
}

func fmtMap(m map[string]any) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(strings.TrimSpace(strings.Join(strings.Fields(toString(v)), " ")))
		b.WriteString(" ")
	}
	return b.String()
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
