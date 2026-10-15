package store_test

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// These tests run against a real PostgreSQL, deliberately.
//
// Everything interesting about this store lives in the database: the row lock
// on the household sequence, the composite foreign keys that make
// cross-household writes impossible, the partial unique index behind the series
// invariant. A fake would test the fake.
//
// Without CHOREO_TEST_DATABASE_URL they skip rather than fail, so `go test
// ./...` still works on a machine with no Docker. `make test-db` sets it up and
// `make test-server` passes it in.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()

	dsn := os.Getenv("CHOREO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CHOREO_TEST_DATABASE_URL is unset — run `make test-db`, then `make test-server`")
	}

	ctx := t.Context()

	if err := store.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrating the test database: %v", err)
	}

	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	t.Cleanup(s.Close)

	truncateAll(t, dsn)
	return s
}

// truncateAll empties every table the migrations created, so each test starts
// from nothing without paying for a fresh database.
//
// The table list is read from the catalogue rather than hard-coded: a new table
// added by a later migration gets cleaned without anyone remembering to come
// back here.
func truncateAll(t *testing.T, dsn string) {
	t.Helper()

	ctx := t.Context()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to truncate: %v", err)
	}
	defer pool.Close()

	const list = `SELECT string_agg(format('%I', tablename), ', ')
	                FROM pg_tables
	               WHERE schemaname = 'public'
	                 AND tablename <> 'goose_db_version'`

	var tables *string
	if err := pool.QueryRow(ctx, list).Scan(&tables); err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	if tables == nil {
		return
	}

	if _, err := pool.Exec(ctx, "TRUNCATE "+*tables+" CASCADE"); err != nil {
		t.Fatalf("truncating: %v", err)
	}
}

// execSQL reaches past the store to set up a state the API cannot produce —
// ageing a timestamp, mostly. Tests may do this; production code may not, and
// the architecture test is what keeps that line.
func execSQL(t *testing.T, query string, args ...any) {
	t.Helper()

	ctx := t.Context()

	pool, err := pgxpool.New(ctx, os.Getenv("CHOREO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("connecting for setup: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, query, args...); err != nil {
		t.Fatalf("setup query: %v", err)
	}
}

// queryRow reads a single integer straight from the database, for assertions
// about columns the store deliberately does not expose — a rev, mostly.
func queryRow(t *testing.T, query string, args ...any) int64 {
	t.Helper()

	ctx := t.Context()

	pool, err := pgxpool.New(ctx, os.Getenv("CHOREO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("connecting for an assertion: %v", err)
	}
	defer pool.Close()

	var value int64
	if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
		t.Fatalf("assertion query: %v", err)
	}
	return value
}

// account creates an account and returns its id.
func account(t *testing.T, s *store.Store, login string) uuid.UUID {
	t.Helper()

	id, err := s.Identity.CreateAccount(t.Context(), login, "$argon2id$fake", "$argon2id$fake")
	if err != nil {
		t.Fatalf("creating account %q: %v", login, err)
	}
	return id
}

// device registers an installation for an account.
func device(t *testing.T, s *store.Store, accountID uuid.UUID) uuid.UUID {
	t.Helper()

	id, err := s.Identity.RegisterDevice(t.Context(), accountID, "1.0.0", 1, "android")
	if err != nil {
		t.Fatalf("registering a device: %v", err)
	}
	return id
}

// session stores a token pair and returns the plain digests used for it.
func session(t *testing.T, s *store.Store, accountID, deviceID uuid.UUID) (access, refresh []byte) {
	t.Helper()

	access = []byte("access-" + uuid.NewString())
	refresh = []byte("refresh-" + uuid.NewString())

	_, err := s.Identity.CreateSession(t.Context(), store.NewSession{
		AccountID:    accountID,
		DeviceID:     deviceID,
		AccessHash:   access,
		RefreshHash:  refresh,
		AccessUntil:  time.Now().Add(15 * time.Minute),
		RefreshUntil: time.Now().Add(180 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}
	return access, refresh
}

// rotate performs one rotation and returns the digests of the new pair.
func rotate(t *testing.T, s *store.Store, refresh []byte) (access, newRefresh []byte) {
	t.Helper()

	prev, err := s.Identity.RotateRefresh(t.Context(), refresh)
	if err != nil {
		t.Fatalf("rotating: %v", err)
	}

	access = []byte("access-" + uuid.NewString())
	newRefresh = []byte("refresh-" + uuid.NewString())

	_, err = s.Identity.CreateSession(t.Context(), store.NewSession{
		AccountID:    prev.AccountID,
		DeviceID:     prev.DeviceID,
		AccessHash:   access,
		RefreshHash:  newRefresh,
		AccessUntil:  time.Now().Add(15 * time.Minute),
		RefreshUntil: time.Now().Add(180 * 24 * time.Hour),
		RotatedFrom:  &prev.ID,
	})
	if err != nil {
		t.Fatalf("storing the successor: %v", err)
	}
	return access, newRefresh
}
