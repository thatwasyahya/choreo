package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SnapshotHeader opens the stream. The client needs the cursor before the rows,
// so it can store it the moment the stream completes.
type SnapshotHeader struct {
	CursorSeq       int64
	PurgeHorizonSeq int64
	TZ              string
}

// SnapshotRow is one line of the stream.
type SnapshotRow struct {
	Entity string
	RowID  uuid.UUID
	Rev    int64
	State  map[string]any
}

// snapshotRows selects LIVE rows only. No tombstones: there is no need to learn
// about the deletion of a row one has never heard of.
//
// That omission is also what makes a real purge possible. The snapshot is a
// closed world — the client drops any local row of a synchronised entity that
// is absent from it, except the ones its own outbox still references — so a row
// deleted a hundred days ago finally disappears from a phone that was away.
const snapshotRows = `
    SELECT 'member' AS entity, m.id AS row_id, m.rev,
           jsonb_build_object(
               'role', m.role,
               'removed', m.removed_rev IS NOT NULL,
               'fields', m.fields,
               'field_revs', m.field_revs) AS state
      FROM members m
     WHERE m.household_id = $1 AND m.deleted_rev IS NULL

    UNION ALL

    SELECT 'series', s.id, s.rev,
           jsonb_build_object(
               'title', s.title, 'mode', s.mode, 'rule', s.rule,
               'rel_days', s.rel_days, 'anchor_on', s.anchor_on,
               'rotation', s.rotation, 'rotation_offset', s.rotation_offset,
               'paused', s.paused, 'catch_up', s.catch_up,
               'next_index', s.next_index,
               'open_occurrence_id', s.open_occurrence_id,
               'ext', s.ext, 'field_revs', s.field_revs)
      FROM series s
     WHERE s.household_id = $1 AND s.deleted_rev IS NULL

    UNION ALL

    SELECT 'task', t.id, t.rev,
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
               ), '[]'::jsonb))
      FROM tasks t
     WHERE t.household_id = $1 AND t.deleted_rev IS NULL

    UNION ALL

    SELECT 'list', l.id, l.rev,
           jsonb_build_object('name', l.name, 'kind', l.kind,
                              'ext', l.ext, 'field_revs', l.field_revs)
      FROM lists l
     WHERE l.household_id = $1 AND l.deleted_rev IS NULL

    UNION ALL

    SELECT 'list_item', i.id, i.rev,
           jsonb_build_object('list_id', i.list_id, 'label', i.label,
                              'qty', i.qty, 'unit', i.unit,
                              'order_key', i.order_key,
                              'ext', i.ext, 'field_revs', i.field_revs)
      FROM list_items i
     WHERE i.household_id = $1 AND i.deleted_rev IS NULL

    UNION ALL

    SELECT 'memo', o.id, o.rev,
           jsonb_build_object('title', o.title, 'body', o.body,
                              'pinned', o.pinned,
                              'ext', o.ext, 'field_revs', o.field_revs)
      FROM memos o
     WHERE o.household_id = $1 AND o.deleted_rev IS NULL

    UNION ALL

    -- Unresolved notices only. A resolved one is history, and in a closed world
    -- the client dropping it locally is the right outcome.
    SELECT 'notice', n.id, n.rev,
           jsonb_build_object(
               'for_member', n.for_member, 'kind', n.kind, 'code', n.code,
               'entity', n.entity, 'row_id', n.row_id,
               'mine', n.mine, 'theirs', n.theirs,
               'by_member', n.by_member, 'op_id', n.op_id,
               'resolved', false)
      FROM notices n
     WHERE n.household_id = $1 AND n.resolved_rev IS NULL

 ORDER BY rev`

// Snapshot streams every live row of a household.
//
// The whole thing runs in ONE repeatable-read transaction, and the cursor is
// read inside it. That is not tidiness: without it, a write that commits while
// the stream is in flight is absent from the body yet carries a rev BELOW the
// cursor handed back. It would never be redelivered, and the device would be
// wrong about its household for good, silently.
//
// Rows are emitted through a callback rather than collected: a household is a
// few hundred kilobytes, but the server must not be the place where that
// assumption is load-bearing.
func (s *Store) Snapshot(
	ctx context.Context, householdID uuid.UUID,
	begin func(SnapshotHeader) error,
	emit func(SnapshotRow) error,
) (int, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return 0, fmt.Errorf("store: beginning the snapshot transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const position = `SELECT change_seq, purge_horizon_seq, tz
	                    FROM households WHERE id = $1`

	var header SnapshotHeader
	err = tx.QueryRow(ctx, position, householdID).
		Scan(&header.CursorSeq, &header.PurgeHorizonSeq, &header.TZ)
	switch {
	case errorsIsNoRows(err):
		return 0, ErrHouseholdUnknown
	case err != nil:
		return 0, fmt.Errorf("store: reading the household position: %w", err)
	}

	if err := begin(header); err != nil {
		return 0, err
	}

	rows, err := tx.Query(ctx, snapshotRows, householdID)
	if err != nil {
		return 0, fmt.Errorf("store: streaming a snapshot: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var row SnapshotRow
		if err := rows.Scan(&row.Entity, &row.RowID, &row.Rev, &row.State); err != nil {
			return count, fmt.Errorf("store: scanning a snapshot row: %w", err)
		}
		if err := emit(row); err != nil {
			return count, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("store: reading a snapshot: %w", err)
	}

	return count, nil
}
