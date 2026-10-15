-- Bootstrap migration.
--
-- It carries no domain model on purpose: phase 0 proves the mechanism, the
-- schema comes with phase 1 once docs/SYNC_PROTOCOL.md is settled (lot L0.9).
--
-- Forward only (decision D-013): there is no Down section, deliberately.
-- Rolling a schema backwards while phones still hold the previous version is
-- how data gets lost. To start over locally, drop the database: make db-reset.

-- +goose Up
CREATE TABLE schema_bootstrap (
    id         smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    applied_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE schema_bootstrap IS
    'Single row, written once. Proves the migration chain ran on this database.';

INSERT INTO schema_bootstrap (id) VALUES (1);
