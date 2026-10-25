package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// opCreateSeries makes a recurring chore, and opens its first occurrence in
// the same transaction.
//
// Both, or neither. A series without an open occurrence is a chore that exists
// and never appears, which is worse than no chore at all: the invariant this
// server arbitrates is EXACTLY one open occurrence, and a row born outside it
// would have to be repaired by something, somewhere, later.
//
// Unlike a tick, a schedule that does not parse IS refused here. The person is
// standing in front of the screen that produced it and can fix it in a second
// — whereas refusing a tick would destroy work already done, which is why the
// advance path swallows the same error and yields a dateless occurrence.
func (s *Store) opCreateSeries(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID, in SyncInput, op Op,
) (OpResult, error) {
	if op.RowID == uuid.Nil {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	// These read as verdicts rather than errors on purpose: an operation that
	// does not describe a schedule is refused, and there is nothing to
	// propagate. The transaction is in perfect health.
	plan, title, ok := readSeries(op.Fields)
	if !ok {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	// The rule is checked now, while somebody can still fix it.
	if !scheduleWorks(plan) {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	rotation, ok := readRotation(op.Fields["rotation"])
	if !ok {
		return refusal(op.OpID, ReasonMalformed), nil
	}
	offset := intField(op.Fields, "rotation_offset", 0)

	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	const create = `INSERT INTO series
	    (id, household_id, rev, created_rev, updated_by, title, mode, rule,
	     rel_days, anchor_on, catch_up, rotation, rotation_offset, next_index)
	    VALUES ($1, $2, $3, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 0)
	    ON CONFLICT (household_id, id) DO NOTHING`

	tag, err := tx.Exec(ctx, create,
		op.RowID, householdID, seq, in.MemberID, title, plan.Mode, plan.Rule,
		plan.RelDays, plan.AnchorOn, plan.CatchUp, rotation, offset)
	if err != nil {
		return OpResult{}, fmt.Errorf("store: creating a series: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Two phones created the same series — the same derived id, the same
		// intent. They agree; there is nothing to resolve.
		return OpResult{OpID: op.OpID, Outcome: OutcomeAbsorbed}, nil
	}

	// Occurrence zero, dated at the anchor: the day the household said this
	// starts. Everything after it is computed from the schedule.
	first := OccurrenceID(op.RowID, 0)

	var assignee *uuid.UUID
	if len(rotation) > 0 {
		assignee = &rotation[offset%len(rotation)]
	}

	const open = `INSERT INTO tasks
	    (id, household_id, rev, created_rev, updated_by, kind, title,
	     series_id, series_index, due_on, assignee_id)
	    VALUES ($1, $2, $3, $3, $4, 'task', $5, $6, 0, $7, $8)
	    ON CONFLICT (household_id, id) DO NOTHING`

	if _, err := tx.Exec(ctx, open,
		first, householdID, seq, in.MemberID, title, op.RowID,
		plan.AnchorOn, assignee); err != nil {
		return OpResult{}, fmt.Errorf("store: opening the first occurrence: %w", err)
	}

	const point = `UPDATE series SET open_occurrence_id = $3
	                WHERE household_id = $1 AND id = $2`
	if _, err := tx.Exec(ctx, point, householdID, op.RowID, first); err != nil {
		return OpResult{}, fmt.Errorf("store: pointing at the first occurrence: %w", err)
	}

	return OpResult{OpID: op.OpID, Outcome: OutcomeApplied, Rev: seq}, nil
}

// scheduleWorks says whether a schedule can produce a date at all.
func scheduleWorks(plan Schedule) bool {
	_, err := nextDue(plan, plan.AnchorOn, nil, plan.AnchorOn)
	return err == nil
}

// readSeries pulls a schedule out of an operation's fields.
//
// It answers with a boolean rather than an error because the caller has only
// one thing to say either way — refused, malformed — and an error value would
// invite somebody to propagate a verdict as a failure.
func readSeries(fields map[string]any) (Schedule, string, bool) {
	title, _ := fields["title"].(string)
	if strings.TrimSpace(title) == "" {
		return Schedule{}, "", false
	}

	plan := Schedule{
		Mode:    stringField(fields, "mode", ""),
		Rule:    stringField(fields, "rule", ""),
		CatchUp: stringField(fields, "catch_up", "skip"),
	}

	switch plan.Mode {
	case "calendar", "relative":
	default:
		return Schedule{}, "", false
	}
	if plan.CatchUp != "skip" && plan.CatchUp != "accumulate" {
		return Schedule{}, "", false
	}

	if plan.Mode == "relative" {
		days := intField(fields, "rel_days", 0)
		if days < 1 {
			return Schedule{}, "", false
		}
		plan.RelDays = &days
	}

	// A DATE, as the client proposed it. Unlike a completion there is no
	// bound here: a household is allowed to say a chore starts next summer.
	anchor, err := time.Parse("2006-01-02", stringField(fields, "anchor_on", ""))
	if err != nil {
		return Schedule{}, "", false
	}
	plan.AnchorOn = anchor

	return plan, title, true
}

// readRotation reads the member ids a chore rotates between.
func readRotation(raw any) ([]uuid.UUID, bool) {
	out := []uuid.UUID{}
	if raw == nil {
		return out, true
	}

	list, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		member, err := uuid.Parse(text)
		if err != nil {
			return nil, false
		}
		out = append(out, member)
	}
	return out, true
}

func stringField(fields map[string]any, name, fallback string) string {
	if value, ok := fields[name].(string); ok && value != "" {
		return value
	}
	return fallback
}

// intField reads a number that arrived through JSON, where every number is a
// float64 until somebody says otherwise.
func intField(fields map[string]any, name string, fallback int) int {
	switch value := fields[name].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return fallback
	}
}
