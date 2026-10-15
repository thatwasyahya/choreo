-- The core schema, as specified in docs/SYNC_PROTOCOL.md.
--
-- Two rules govern every table here, and breaking either one breaks the product:
--
--   1. household_id is the FIRST column of every primary key and every unique
--      index. No uniqueness is ever global. A former flatmate who kept a replica
--      knows UUIDs from that household; they must not be able to reserve an id
--      in someone else's household and block its generation.
--
--   2. The sync envelope is IDENTICAL on every synchronised table: rev,
--      created_rev, field_revs, deleted_rev, updated_by, updated_at. One shape,
--      not thirteen variants. updated_by and updated_at are SERVER values,
--      never supplied by a client.
--
-- Tables for later phases are created now and left empty. Adding the isolation
-- boundary after the service is live would mean rebuilding a security boundary
-- in production.
--
-- Forward only (decision D-013): no Down section. To start over locally, drop
-- the database — see `make db-reset`.

-- +goose Up

-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS citext;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- Identity, household, devices
-- ---------------------------------------------------------------------------

CREATE TABLE accounts (
    id            uuid PRIMARY KEY,
    login         citext UNIQUE NOT NULL,
    pw_hash       text NOT NULL,                  -- argon2id (D-009)
    recovery_hash text NOT NULL,                  -- recovery code (D-009)
    created_at    timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE accounts IS
    'No e-mail column, by decision D-009: nothing is required to create and use a household.';

CREATE TABLE households (
    id                uuid PRIMARY KEY,
    name              text NOT NULL,
    tz                text NOT NULL DEFAULT 'Europe/Paris',   -- IANA
    change_seq        bigint NOT NULL DEFAULT 0,
    purge_horizon_seq bigint NOT NULL DEFAULT 0,
    settings          jsonb NOT NULL DEFAULT '{}',            -- opaque to the server
    created_at        timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN households.change_seq IS
    'THE clock of this household. Incremented under a row lock inside the writing '
    'transaction, so assignment order equals commit order and a cursor cannot miss a change. '
    'Deliberately not a PostgreSQL sequence: those leave gaps and commit out of order.';

COMMENT ON COLUMN households.purge_horizon_seq IS
    'Below this, a cursor is dead and the client must take a snapshot.';

-- A member is a person in the household, WITH OR WITHOUT AN ACCOUNT (glossary).
-- account_id NULL = member without an account, who can still be assigned things
-- and claim their profile later.
--
-- Belonging to several households is simply several members rows for one
-- account_id: no separate membership table, and no "current household" anywhere.
CREATE TABLE members (
    id           uuid NOT NULL,
    household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    account_id   uuid NULL REFERENCES accounts(id) ON DELETE SET NULL,
    role         text NOT NULL CHECK (role IN ('admin', 'adult', 'child', 'guest')),
    removed_rev  bigint NULL,                     -- leaving does not rewrite history
    -- sync envelope
    rev         bigint NOT NULL,
    created_rev bigint NOT NULL,
    field_revs  jsonb  NOT NULL DEFAULT '{}',
    deleted_rev bigint NULL,
    updated_by  uuid   NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    fields      jsonb  NOT NULL DEFAULT '{}',     -- display_name, colour, …
    PRIMARY KEY (household_id, id)
);
CREATE INDEX members_account ON members (account_id) WHERE account_id IS NOT NULL;
CREATE INDEX members_seq     ON members (household_id, rev);

CREATE TABLE devices (
    id          uuid PRIMARY KEY,                 -- one per installation
    account_id  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_version text NOT NULL,
    protocol    int  NOT NULL,
    platform    text NOT NULL,
    revoked_at  timestamptz NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- One cursor per (device, household). Not one per device: a carer looking after
-- a parent's household, and alternating custody, are facts of the domain rather
-- than a phase 3 option.
CREATE TABLE device_cursors (
    device_id     uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    household_id  uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    member_id     uuid NOT NULL,
    cursor_seq    bigint NOT NULL DEFAULT 0,      -- 0 = take a snapshot first
    outbox_age_s  int    NOT NULL DEFAULT 0,      -- reported by the client, §2.4
    clock_skew_ms bigint NULL,                    -- observed, never applied
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, household_id)
);

-- ---------------------------------------------------------------------------
-- Domain — series before tasks, because tasks reference series
-- ---------------------------------------------------------------------------

CREATE TABLE series (
    id uuid NOT NULL, household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    rev bigint NOT NULL, created_rev bigint NOT NULL, field_revs jsonb NOT NULL DEFAULT '{}',
    deleted_rev bigint NULL, updated_by uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    title     text NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    mode      text NOT NULL CHECK (mode IN ('calendar', 'relative')),
    rule      text NOT NULL DEFAULT '',          -- RRULE subset, calendar mode
    rel_days  int  NULL,                         -- "X days after the last time"
    anchor_on date NOT NULL,
    rotation        uuid[] NOT NULL DEFAULT '{}',
    rotation_offset int    NOT NULL DEFAULT 0,
    paused   boolean NOT NULL DEFAULT false,
    catch_up text NOT NULL DEFAULT 'skip' CHECK (catch_up IN ('skip', 'accumulate')),
    -- THE TWO ARBITRATED COLUMNS. A client never writes them.
    next_index         int  NOT NULL DEFAULT 0,
    open_occurrence_id uuid NULL,
    ext jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (household_id, id)
);
CREATE INDEX series_seq ON series (household_id, rev);

COMMENT ON COLUMN series.next_index IS
    'An INTEGER, never a date. The occurrence id is uuidv5(series_id || series_index), so two '
    'offline phones compute the SAME id and the second insert becomes ON CONFLICT DO NOTHING. '
    'Deriving it from a computed date would break the invariant during a rolling deploy, on a '
    'tzdata skew between an N-1 app and the server.';

COMMENT ON COLUMN series.open_occurrence_id IS
    'Exactly one open occurrence, NULL if and only if paused or deleted. This column IS the '
    'invariant: no per-row merge rule can express a constraint between rows.';

CREATE TABLE tasks (
    id uuid NOT NULL, household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    rev bigint NOT NULL, created_rev bigint NOT NULL, field_revs jsonb NOT NULL DEFAULT '{}',
    deleted_rev bigint NULL, updated_by uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    closed_rev  bigint NULL,                     -- derived: non-null once a completion exists
    -- content
    kind        text NOT NULL CHECK (kind IN ('task', 'event')),
    title       text NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    notes       text NULL CHECK (notes IS NULL OR length(notes) <= 8000),
    due_on      date NULL,                       -- a DATE, not an instant
    due_time    time NULL,
    all_day     boolean NOT NULL DEFAULT true,
    assignee_id uuid NULL,
    category_id uuid NULL,                       -- categories arrive in phase 2
    priority    smallint NULL,
    -- series
    series_id    uuid NULL,
    series_index int  NULL,                      -- an INTEGER, not a date
    overrides    text[] NOT NULL DEFAULT '{}',   -- fields a human pinned
    ext          jsonb  NOT NULL DEFAULT '{}',   -- fields from a newer app
    PRIMARY KEY (household_id, id),
    -- Composite on purpose: you cannot assign a task to a member of another
    -- household, and the database is what enforces it.
    FOREIGN KEY (household_id, assignee_id) REFERENCES members (household_id, id),
    FOREIGN KEY (household_id, series_id)   REFERENCES series  (household_id, id)
);
CREATE INDEX tasks_seq ON tasks (household_id, rev);
CREATE UNIQUE INDEX tasks_series_slot ON tasks (household_id, series_id, series_index)
    WHERE series_id IS NOT NULL;
-- The last-resort guard against our OWN bugs, never the mechanism:
CREATE UNIQUE INDEX tasks_one_open_per_series ON tasks (household_id, series_id)
    WHERE series_id IS NOT NULL AND deleted_rev IS NULL AND closed_rev IS NULL;

-- Completions are a SET, not a boolean. Added per member, never overwritten.
-- Same mechanism for ticking a task and ticking a list item: one fewer rule to
-- hold in mind, and it is the more used of the two.
CREATE TABLE completions (
    household_id  uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    row_id        uuid NOT NULL,                 -- tasks.id or list_items.id: no FK, polymorphic
    member_id     uuid NOT NULL,
    completion_id uuid NOT NULL,                 -- client-minted: the point dedup key
    done_rev      bigint NOT NULL,
    occurred_on   date   NOT NULL,               -- PROPOSED by the client, BOUNDED by the server
    client_ts     timestamptz NULL,              -- display only, never authority
    clock_suspect boolean NOT NULL DEFAULT false,
    PRIMARY KEY (household_id, row_id, member_id)
);

CREATE TABLE lists (
    id uuid NOT NULL, household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    rev bigint NOT NULL, created_rev bigint NOT NULL, field_revs jsonb NOT NULL DEFAULT '{}',
    deleted_rev bigint NULL, updated_by uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    kind text NOT NULL DEFAULT 'checklist',
    ext  jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (household_id, id)
);
CREATE INDEX lists_seq ON lists (household_id, rev);

CREATE TABLE list_items (
    id uuid NOT NULL, household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    rev bigint NOT NULL, created_rev bigint NOT NULL, field_revs jsonb NOT NULL DEFAULT '{}',
    deleted_rev bigint NULL, updated_by uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    list_id   uuid NOT NULL,
    label     text NOT NULL CHECK (length(label) BETWEEN 1 AND 500),
    qty       numeric NULL,
    unit      text NULL,
    order_key text NOT NULL DEFAULT '',
    ext       jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (household_id, id),
    FOREIGN KEY (household_id, list_id) REFERENCES lists (household_id, id)
);
CREATE INDEX list_items_seq  ON list_items (household_id, rev);
CREATE INDEX list_items_list ON list_items (household_id, list_id);

CREATE TABLE memos (
    id uuid NOT NULL, household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    rev bigint NOT NULL, created_rev bigint NOT NULL, field_revs jsonb NOT NULL DEFAULT '{}',
    deleted_rev bigint NULL, updated_by uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    title  text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    body   text NOT NULL DEFAULT '' CHECK (length(body) <= 8000),
    pinned boolean NOT NULL DEFAULT false,
    ext    jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (household_id, id)
);
CREATE INDEX memos_seq ON memos (household_id, rev);

-- Tags and completions TRAVEL INSIDE their task's state and have no rev of their
-- own: ticking a task bumps the task's rev. Only seven top-level entities are
-- therefore queried by a pull.
CREATE TABLE tags (
    household_id uuid   NOT NULL,
    task_id      uuid   NOT NULL,
    tag          citext NOT NULL CHECK (length(tag) BETWEEN 1 AND 60),
    add_rev      bigint NOT NULL,
    PRIMARY KEY (household_id, task_id, tag),
    FOREIGN KEY (household_id, task_id) REFERENCES tasks (household_id, id) ON DELETE CASCADE
);

-- ---------------------------------------------------------------------------
-- Notices — one surface, on the SERVER
-- ---------------------------------------------------------------------------

-- A notice lives on the server, so it appears on the phone AND the tablet of the
-- same member, survives a reinstall, and survives the loss of the device that
-- caused it. Keeping refused content in a local table means keeping it on
-- exactly the device that can disappear.
CREATE TABLE notices (
    id uuid NOT NULL, household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    for_member uuid NOT NULL,                    -- ADDRESSED. Never "to the household"
    kind   text NOT NULL CHECK (kind IN ('receipt', 'decision', 'refusal')),
    code   text NOT NULL,                        -- a localisation key, never a sentence
    entity text NOT NULL,
    row_id uuid NULL,
    mine   jsonb NOT NULL,                       -- the user's content, VERBATIM
    theirs jsonb NULL,                           -- the value that held
    by_member uuid NULL,
    op_id     uuid NULL,
    rev bigint NOT NULL, created_rev bigint NOT NULL,
    resolved_rev    bigint NULL,
    resolved_choice text NULL,
    PRIMARY KEY (household_id, id)
);
CREATE INDEX notices_seq ON notices (household_id, rev);
CREATE INDEX notices_open ON notices (household_id, for_member)
    WHERE resolved_rev IS NULL;

-- ---------------------------------------------------------------------------
-- The operation ledger — the 2am table
-- ---------------------------------------------------------------------------

-- No foreign keys here, on purpose: an audit log has to survive the purge of the
-- rows it talks about.
CREATE TABLE ops (
    household_id uuid NOT NULL,
    op_id        uuid NOT NULL,                  -- client-minted at the moment of the gesture
    payload_hash bytea NOT NULL,                 -- sha256 of the canonical payload
    device_id    uuid NOT NULL,
    member_id    uuid NOT NULL,                  -- FROM THE TOKEN, never from the body
    kind         text NOT NULL,
    row_id       uuid NULL,
    payload      jsonb NOT NULL,                 -- VERBATIM what the client sent
    outcome      text NOT NULL CHECK (outcome IN
                   ('applied', 'absorbed', 'receipted', 'superseded', 'refused')),
    reason       text NULL,
    result_rev   bigint NULL,
    result       jsonb NULL,                     -- the rendered response, replayed identically
    client_ts    timestamptz NULL,               -- the phone's clock. Stored. Never believed
    app_version  text NOT NULL,
    protocol     int  NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (household_id, op_id)
);
CREATE INDEX ops_recent ON ops (household_id, received_at DESC);
CREATE INDEX ops_by_row ON ops (household_id, row_id, received_at DESC);

COMMENT ON COLUMN ops.payload_hash IS
    'Compared on every replay. A client bug that reuses an op_id for a different operation '
    'gets malformed, not the other operation''s answer. Without this hash a mutation vanishes '
    'with no trace at all.';
