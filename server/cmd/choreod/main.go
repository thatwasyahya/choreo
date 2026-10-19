// Command choreod is the Choreo server.
//
// It is a sync relay, not the brain: phones hold a complete replica of their
// household. See docs/ARCHITECTURE.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/config"
	"github.com/thatwasyahya/choreo/server/internal/httpapi"
	"github.com/thatwasyahya/choreo/server/internal/obs"
	"github.com/thatwasyahya/choreo/server/internal/store"
	"github.com/thatwasyahya/choreo/server/internal/wake"
)

// version is stamped at build time with -ldflags "-X main.version=...", which
// only works on a package-level var.
//
//nolint:gochecknoglobals // written once by the linker, read-only afterwards
var version = "dev"

func main() {
	if err := run(); err != nil {
		// The structured logger may not exist yet, so this one goes out raw.
		fmt.Fprintf(os.Stderr, "choreod: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// A deploy applies the schema as its own step, then starts the server.
	// Keeping the two separable is what lets a migration fail loudly instead
	// of inside a crash loop.
	migrateOnly := flag.Bool("migrate-only", false,
		"apply pending migrations, then exit without serving")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := obs.NewLogger(cfg.LogLevel)
	logger.Info("starting",
		slog.String("version", version),
		slog.Any("config", cfg.Redacted()),
	)

	// A termination signal cancels this context, which unwinds everything
	// below it, including a migration that is still running.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	logger.Info("schema up to date")

	if *migrateOnly {
		return nil
	}

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	metrics := obs.NewMetrics()

	// One LISTEN connection for the whole process, outside the pool. It is what
	// lets a sync call park on a goroutine instead of on a database connection.
	hub := wake.New(cfg.DatabaseURL, logger)
	go hub.Run(ctx)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.New(httpapi.Deps{
			Logger:    logger,
			Metrics:   metrics,
			DB:        db,
			Version:   version,
			Store:     db,
			Verifier:  auth.NewVerifier(auth.DefaultHashConcurrency),
			Limits:    httpapi.DefaultLimits(),
			PublicURL: cfg.PublicURL,
			Wake:      hub,
		}),
		// Without this, a client that opens a connection and never sends a
		// request header holds a goroutine forever.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", slog.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("http server: %w", err)
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("signal received, draining",
			slog.String("timeout", cfg.ShutdownTimeout.String()))
	}

	// Deliberately not derived from ctx: that one is already cancelled, and
	// in-flight requests are owed their chance to finish.
	drainCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(drainCtx); err != nil {
		return fmt.Errorf("draining the http server: %w", err)
	}

	logger.Info("stopped")
	return nil
}
