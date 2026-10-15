// Package store owns the PostgreSQL connection and the schema migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver, needed by goose
	"github.com/pressly/goose/v3"
)

// Migrations travel inside the binary. A server that carries its own schema
// cannot be deployed against the wrong set of files.
//
//go:embed migrations/*.sql
//nolint:gochecknoglobals // embedded at compile time, read-only
var migrations embed.FS

// Store is the server's access to household-scoped data.
//
// Every exported method takes householdID right after the context, and an
// architecture test fails the build if one does not: the tenant cannot be
// forgotten if the compiler asks for it. See internal/arch.
type Store struct {
	pool *pgxpool.Pool

	// Identity is the non-tenant half: accounts, devices and sessions exist
	// before any household and can belong to several, so the householdID rule
	// does not apply there. A field rather than a method, so the distinction is
	// visible at every call site.
	Identity *Identity
}

// Open connects and verifies the connection before returning, so a bad
// configuration fails at startup rather than on the first user request.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	return &Store{pool: pool, Identity: &Identity{pool: pool}}, nil
}

// Ping reports whether PostgreSQL is reachable right now.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("pinging postgres: %w", err)
	}
	return nil
}

// Close releases the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}

// Migrate applies every pending migration.
//
// Forward only (decision D-013): there is no Down path, because rolling a
// schema backwards while phones still hold the old one is how data gets lost.
// To start over locally, drop the database — see `make db-reset`.
func Migrate(ctx context.Context, url string) error {
	goose.SetBaseFS(migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("selecting the goose dialect: %w", err)
	}

	// goose needs a database/sql handle; the pgx stdlib driver provides one.
	// It is short-lived and closed as soon as the schema is current.
	db, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("opening postgres for migrations: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}
