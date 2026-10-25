package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// pullPage is one query, not one per entity.
//
// A UNION ALL with a single global ORDER BY is the only way to paginate this
// correctly. Querying each entity separately and merging in Go looks simpler
// until one entity overflows its own limit: its surplus rows sit below the
// cut-off revision, the cursor advances past them, and they are lost for good.
//
// Completions and tags are aggregated INTO the state of the row they belong
// to rather than travelling as their own entities: a tick bumps that row's rev,
// so it has no revision of its own and cannot be paginated away from the thing
// it is a fact about.
const pullPage = `
WITH page AS (
    SELECT 'task' AS entity, t.id AS row_id, t.rev,
           t.deleted_rev IS NOT NULL AS deleted,
           t.updated_by, t.updated_at,
           jsonb_build_object(
               'kind', t.kind, 'title', t.title, 'notes', t.notes,
               'due_on', t.due_on, 'due_time', t.due_time, 'all_day', t.all_day,
               'assignee_id', t.assignee_id, 'category_id', t.category_id,
               'priority', t.priority,
               'series_id', t.series_id, 'series_index', t.series_index,
               'overrides', t.overrides,
               'ext', t.ext, 'field_revs', t.field_revs,
               'completions', COALESCE((
                   SELECT jsonb_agg(jsonb_build_object(
                              'member_id', c.member_id,
                              'completion_id', c.completion_id,
                              'occurred_on', c.occurred_on,
                              'clock_suspect', c.clock_suspect))
                     FROM completions c
                    WHERE c.household_id = t.household_id AND c.row_id = t.id
               ), '[]'::jsonb),
               'tags', COALESCE((
                   SELECT jsonb_agg(g.tag ORDER BY g.tag)
                     FROM tags g
                    WHERE g.household_id = t.household_id AND g.task_id = t.id
               ), '[]'::jsonb)
           ) AS state
      FROM tasks t
     WHERE t.household_id = $1 AND t.rev > $2

    UNION ALL

    -- Series. They were in the snapshot and not here, so a recurring chore
    -- reached a phone only when it rebuilt its whole replica — which is to
    -- say, almost never. The acceptance test that creates one over HTTP is
    -- what caught it.
    SELECT 'series', s.id, s.rev,
           s.deleted_rev IS NOT NULL,
           s.updated_by, s.updated_at,
           jsonb_build_object(
               'title', s.title,
               'mode', s.mode,
               'rule', s.rule,
               'rel_days', s.rel_days,
               'anchor_on', s.anchor_on,
               'rotation', s.rotation,
               'rotation_offset', s.rotation_offset,
               'paused', s.paused,
               'catch_up', s.catch_up,
               -- Arbitrated here and mirrored there: a client reads these two
               -- and never writes them.
               'next_index', s.next_index,
               'open_occurrence_id', s.open_occurrence_id,
               'field_revs', s.field_revs,
               'ext', s.ext)
      FROM series s
     WHERE s.household_id = $1 AND s.rev > $2

    UNION ALL

    -- Lists and their items. Both were in the snapshot and in neither page,
    -- the same hole series fell into: a shared shopping list reached a second
    -- phone only when that phone rebuilt its whole replica.
    SELECT 'list', l.id, l.rev,
           l.deleted_rev IS NOT NULL,
           l.updated_by, l.updated_at,
           jsonb_build_object(
               'name', l.name,
               'kind', l.kind,
               'ext', l.ext,
               'field_revs', l.field_revs)
      FROM lists l
     WHERE l.household_id = $1 AND l.rev > $2

    UNION ALL

    SELECT 'list_item', i.id, i.rev,
           i.deleted_rev IS NOT NULL,
           i.updated_by, i.updated_at,
           jsonb_build_object(
               'list_id', i.list_id,
               'label', i.label,
               'qty', i.qty,
               'unit', i.unit,
               -- Fractional, and a string: two people reordering the same list
               -- in the same minute both get their move.
               'order_key', i.order_key,
               'ext', i.ext,
               'field_revs', i.field_revs,
               -- A tick carries WHO, not merely that somebody did it: two
               -- trolleys in the same shop need to see each other.
                          'completions', COALESCE((
                   SELECT jsonb_agg(jsonb_build_object(
                              'member_id', c.member_id,
                              'completion_id', c.completion_id,
                              'occurred_on', c.occurred_on,
                              'clock_suspect', c.clock_suspect))
                     FROM completions c
                    WHERE c.household_id = i.household_id AND c.row_id = i.id
               ), '[]'::jsonb))
      FROM list_items i
     WHERE i.household_id = $1 AND i.rev > $2

    UNION ALL

    SELECT 'member', m.id, m.rev,
           m.deleted_rev IS NOT NULL,
           m.updated_by, m.updated_at,
           jsonb_build_object(
               'role', m.role,
               'removed', m.removed_rev IS NOT NULL,
               'fields', m.fields,
               'field_revs', m.field_revs)
      FROM members m
     WHERE m.household_id = $1 AND m.rev > $2

    UNION ALL

    SELECT 'notice', n.id, n.rev,
           false,
           NULL::uuid, NULL::timestamptz,
           jsonb_build_object(
               'for_member', n.for_member,
               'kind', n.kind, 'code', n.code,
               'entity', n.entity, 'row_id', n.row_id,
               'mine', n.mine, 'theirs', n.theirs,
               'by_member', n.by_member,
               'op_id', n.op_id,
               'resolved', n.resolved_rev IS NOT NULL,
               'resolved_choice', n.resolved_choice)
      FROM notices n
     WHERE n.household_id = $1 AND n.rev > $2
)
SELECT entity, row_id, rev, deleted, updated_by, updated_at, state
  FROM page
 ORDER BY rev, entity, row_id
 LIMIT $3`

// pull returns everything that changed after a cursor.
func (s *Store) pull(
	ctx context.Context, householdID uuid.UUID, cursor int64, limit int,
) (SyncOutput, error) {
	if limit <= 0 || limit > maxPullLimit {
		limit = defaultPullLimit
	}

	head, horizon, err := s.position(ctx, householdID)
	if err != nil {
		return SyncOutput{}, err
	}

	// One extra row, so a full page can be told from a page that merely
	// happened to land on the limit.
	rows, err := s.pool.Query(ctx, pullPage, householdID, cursor, limit+1)
	if err != nil {
		return SyncOutput{}, fmt.Errorf("store: pulling changes: %w", err)
	}
	defer rows.Close()

	changes := make([]Change, 0, limit)
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.Entity, &c.RowID, &c.Rev, &c.Deleted,
			&c.UpdatedBy, &c.UpdatedAt, &c.State); err != nil {
			return SyncOutput{}, fmt.Errorf("store: scanning a change: %w", err)
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return SyncOutput{}, fmt.Errorf("store: reading changes: %w", err)
	}

	hasMore := len(changes) > limit
	if hasMore {
		changes = cutOnRevBoundary(changes, limit)
	}

	next := cursor
	if len(changes) > 0 {
		next = changes[len(changes)-1].Rev
	}

	return SyncOutput{
		HeadSeq:   head,
		Changes:   changes,
		CursorSeq: next,
		HasMore:   hasMore,

		PurgeHorizonSeq: horizon,
		// A cursor of zero is a NEW device, not a broken one: taking a snapshot
		// first is its normal starting state, and answering it with an error
		// would be answering a non-problem.
		SnapshotRequired: cursor > 0 && cursor < horizon,
	}, nil
}

// cutOnRevBoundary truncates a page without splitting a revision.
//
// One operation can write a row AND a notice at the same revision. Cutting
// between them would advance the cursor past the half that was dropped, and it
// would never be sent again — a notice silently lost is exactly the failure
// the server-side notice table exists to prevent.
func cutOnRevBoundary(changes []Change, limit int) []Change {
	excluded := changes[limit].Rev
	page := changes[:limit]

	for len(page) > 0 && page[len(page)-1].Rev == excluded {
		page = page[:len(page)-1]
	}

	if len(page) == 0 {
		// Every row in the page shares one revision. Send them all rather than
		// nothing: a page that cannot advance is a client that never catches up.
		return changes[:limit]
	}
	return page
}

// position reads the household's head and purge horizon.
func (s *Store) position(ctx context.Context, householdID uuid.UUID) (head, horizon int64, err error) {
	const q = `SELECT change_seq, purge_horizon_seq FROM households WHERE id = $1`

	if err := s.pool.QueryRow(ctx, q, householdID).Scan(&head, &horizon); err != nil {
		if errorsIsNoRows(err) {
			return 0, 0, ErrHouseholdUnknown
		}
		return 0, 0, fmt.Errorf("store: reading the household position: %w", err)
	}
	return head, horizon, nil
}
