-- Sessions and invitations, as specified in docs/AUTH.md.
--
-- Accounts, devices and members already exist (00002). This adds only what
-- authentication needs on top.
--
-- Forward only (decision D-013): no Down section.

-- +goose Up

-- A live refresh token. The access token is never stored in readable form
-- either: it is verified by recomputing its hash against this row.
CREATE TABLE sessions (
    id           uuid PRIMARY KEY,
    account_id   uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    device_id    uuid NOT NULL REFERENCES devices(id)  ON DELETE CASCADE,
    access_hash  bytea NOT NULL,
    refresh_hash bytea NOT NULL,
    access_expires_at  timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL,
    -- Rotation chain: this is what detects the replay of a consumed refresh.
    rotated_from uuid NULL,
    consumed_at  timestamptz NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX sessions_access  ON sessions (access_hash);
CREATE UNIQUE INDEX sessions_refresh ON sessions (refresh_hash);
CREATE INDEX sessions_device ON sessions (device_id);

COMMENT ON COLUMN sessions.access_hash IS
    'sha256, not argon2id. Thirty-two random bytes have nothing to brute-force, so there is '
    'nothing to slow down. A password does, which is why that one is argon2id.';

COMMENT ON COLUMN sessions.consumed_at IS
    'A refresh token is single-use. Replaying a consumed one means either the thief or the '
    'victim is using it, and we cannot tell which — so the whole device is revoked.';

CREATE TABLE invites (
    id           uuid PRIMARY KEY,
    household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    code_hash    bytea NOT NULL,
    -- Non-null = this code CLAIMS an existing account-less member. The chores
    -- already assigned to "Lina" stay hers once Lina finally has a phone.
    member_id    uuid NULL,
    role         text NOT NULL CHECK (role IN ('adult', 'child', 'guest')),
    created_by   uuid NOT NULL,
    expires_at   timestamptz NOT NULL,
    redeemed_at  timestamptz NULL,
    redeemed_by  uuid NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX invites_code ON invites (code_hash);
CREATE INDEX invites_live ON invites (household_id) WHERE redeemed_at IS NULL;

COMMENT ON TABLE invites IS
    'An invite code never grants admin: the CHECK forbids it. One becomes admin by creating '
    'the household, or by explicit promotion. A leaked code cannot hand over the household.';
