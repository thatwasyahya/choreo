package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// opSetFields applies a field write, field by field, causally.
//
// "Concurrent" here means "had not seen the current value", never "has an older
// timestamp". That distinction is the whole reason a wrong phone clock cannot
// win an argument: a handset set to 1997 or to 2031 is judged on what it had
// actually read, which is a fact about our own sequence.
//
// The rules applied, from the table in docs/SYNC_PROTOCOL.md:
//
//	base_rev == field_rev        → applied. Over 99% of traffic; no conflict exists
//	field_rev absent             → applied. Nothing to conflict with
//	base_rev <  field_rev,
//	or absent while field_rev    → the SERVER's value holds, receipted, notice to
//	                               the author carrying their text intact
//	base_rev >  field_rev        → malformed. Impossible, so it is our bug
//	row unknown                  → refused target_gone. A field write NEVER
//	                               creates a row
//	row deleted                  → the deletion holds the display, the edit is not
//	                               thrown away, decision notice to the editor
func (s *Store) opSetFields(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID, in SyncInput, op Op,
) (OpResult, error) {
	if op.Entity != EntityTask {
		return refusal(op.OpID, ReasonUnsupportedOp), nil
	}
	if len(op.Fields) == 0 || op.RowID == uuid.Nil {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	const load = `SELECT rev, field_revs, deleted_rev IS NOT NULL, updated_by, to_jsonb(t)
	                FROM tasks t
	               WHERE household_id = $1 AND id = $2
	                 FOR UPDATE`

	var (
		currentRev int64
		fieldRevs  map[string]int64
		deleted    bool
		heldBy     uuid.UUID
		row        map[string]any
	)
	err := tx.QueryRow(ctx, load, householdID, op.RowID).
		Scan(&currentRev, &fieldRevs, &deleted, &heldBy, &row)
	switch {
	case errorsIsNoRows(err):
		// The content is kept in the ledger, and the author gets a card: they
		// typed something the server could not put anywhere, and that must not
		// disappear quietly. Without this rule a purged row would resurrect
		// piecemeal, and the purge would destroy the evidence an arbiter needs.
		return s.refuseAndTell(
			ctx, tx, householdID, in, op, ReasonTargetGone, op.Fields)
	case err != nil:
		return OpResult{}, fmt.Errorf("store: loading a task: %w", err)
	}

	if deleted {
		// Nobody is wrong. But the deleter already has what they asked for, so
		// one voice decides — the one whose work is at stake.
		return s.noticeOnly(ctx, tx, householdID, in, op, noticeSpec{
			kind:   "decision",
			code:   "edit_vs_delete",
			mine:   op.Fields,
			theirs: nil,
			byWhom: &heldBy,
		}, currentRev)
	}

	plan, err := classify(op, fieldRevs, row)
	if err != nil {
		return OpResult{}, err
	}
	if plan.malformed {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	rev := currentRev
	if len(plan.applyKnown) > 0 || len(plan.applyUnknown) > 0 {
		if err := s.writeFields(ctx, tx, householdID, in, op, plan, seq); err != nil {
			return OpResult{}, err
		}
		rev = seq
	}

	result := OpResult{
		OpID:    op.OpID,
		Outcome: OutcomeApplied,
		Rev:     rev,
		Fields:  plan.perField,
	}

	if len(plan.superseded) > 0 {
		result.Outcome = OutcomeReceipted
		result.Code = ReasonFieldSuperseded

		noticeID, err := s.writeNotice(ctx, tx, householdID, in, op, noticeSpec{
			kind:   "receipt",
			code:   ReasonFieldSuperseded,
			mine:   plan.superseded,
			theirs: plan.theirs,
			byWhom: &heldBy,
		}, seq)
		if err != nil {
			return OpResult{}, err
		}
		result.NoticeID = &noticeID
	}

	return result, nil
}

// fieldPlan is the decision taken for each field of one operation.
type fieldPlan struct {
	applyKnown   map[string]any // coerced, destined for real columns
	applyUnknown map[string]any // destined for ext
	superseded   map[string]any // the author's text, kept for the notice
	theirs       map[string]any // the values that held
	perField     map[string]string
	malformed    bool
}

// classify decides each field without touching the database.
//
// Separated from the write so the rules can be read in one place and tested
// without a transaction.
func classify(op Op, fieldRevs map[string]int64, row map[string]any) (fieldPlan, error) {
	plan := fieldPlan{
		applyKnown:   map[string]any{},
		applyUnknown: map[string]any{},
		superseded:   map[string]any{},
		theirs:       map[string]any{},
		perField:     map[string]string{},
	}

	for name, value := range op.Fields {
		if name == "kind" {
			// A task does not become an event by a field write.
			plan.malformed = true
			return plan, nil
		}

		seen := op.BaseRevs[name] // absent reads as zero
		held := fieldRevs[name]

		switch {
		case seen > held:
			// The author claims to have read a revision that was never issued
			// for this field. Impossible, therefore ours to fix.
			plan.malformed = true
			return plan, nil

		case held == 0, seen == held:
			_, cast, known := taskColumn(name)
			if !known {
				plan.applyUnknown[name] = value
			} else {
				coerced, err := coerce(cast, value)
				if err != nil {
					return plan, err
				}
				plan.applyKnown[name] = coerced
			}
			plan.perField[name] = "applied"

		default:
			// seen < held: first-writer-wins. A value already seen synchronised
			// does not change under anyone's eyes, and a phone resurfacing
			// after ten days cannot overwrite the household's morning.
			plan.superseded[name] = value
			plan.theirs[name] = row[name]
			plan.perField[name] = "receipt"
		}
	}
	return plan, nil
}

// writeFields updates the columns a plan decided to apply.
func (s *Store) writeFields(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID,
	in SyncInput, op Op, plan fieldPlan, seq int64,
) error {
	revs := map[string]int64{}
	for name := range plan.applyKnown {
		revs[name] = seq
	}
	for name := range plan.applyUnknown {
		revs[name] = seq
	}

	fieldRevs, err := jsonOf(revs)
	if err != nil {
		return err
	}
	ext, err := jsonOf(plan.applyUnknown)
	if err != nil {
		return err
	}

	// $1 household, $2 row, $3 rev, $4 updated_by, $5 field_revs, $6 ext
	sets := []string{
		"rev = $3",
		"updated_by = $4",
		"updated_at = now()",
		"field_revs = field_revs || $5::jsonb",
		"ext = ext || $6::jsonb",
	}
	values := []any{householdID, op.RowID, seq, in.MemberID, fieldRevs, ext}

	for name, value := range plan.applyKnown {
		column, cast, _ := taskColumn(name)
		values = append(values, value)
		sets = append(sets, column+" = $"+strconv.Itoa(len(values))+"::"+cast)
	}

	//nolint:gosec // every column name comes from taskColumn's closed switch
	query := "UPDATE tasks SET " + strings.Join(sets, ", ") +
		" WHERE household_id = $1 AND id = $2"

	if _, err := tx.Exec(ctx, query, values...); err != nil {
		return fmt.Errorf("store: writing task fields: %w", err)
	}
	return nil
}

// noticeSpec is one card for the "to review" tray.
type noticeSpec struct {
	kind   string
	code   string
	mine   map[string]any
	theirs map[string]any
	byWhom *uuid.UUID
}

// writeNotice records a notice ON THE SERVER.
//
// Not in a local table: a notice has to appear on the phone and on the tablet
// of the same member, survive a reinstall, and survive the loss of the device
// that caused it. Keeping refused content locally means keeping it on exactly
// the device that can disappear.
func (s *Store) writeNotice(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID,
	in SyncInput, op Op, spec noticeSpec, seq int64,
) (uuid.UUID, error) {
	mine, err := jsonOf(spec.mine)
	if err != nil {
		return uuid.Nil, err
	}

	var theirs []byte
	if spec.theirs != nil {
		if theirs, err = jsonOf(spec.theirs); err != nil {
			return uuid.Nil, err
		}
	}

	const q = `INSERT INTO notices
	    (id, household_id, for_member, kind, code, entity, row_id,
	     mine, theirs, by_member, op_id, rev, created_rev)
	    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`

	id := uuid.New()
	if _, err := tx.Exec(ctx, q,
		id, householdID, in.MemberID, spec.kind, spec.code, op.Entity,
		nullUUID(op.RowID), mine, theirs, spec.byWhom, op.OpID, seq,
	); err != nil {
		return uuid.Nil, fmt.Errorf("store: writing a notice: %w", err)
	}
	return id, nil
}

// noticeOnly records a notice and changes no row, for the cases where the
// server's state already holds and only the person needs telling.
func (s *Store) noticeOnly(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID,
	in SyncInput, op Op, spec noticeSpec, rev int64,
) (OpResult, error) {
	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	noticeID, err := s.writeNotice(ctx, tx, householdID, in, op, spec, seq)
	if err != nil {
		return OpResult{}, err
	}

	return OpResult{
		OpID:     op.OpID,
		Outcome:  OutcomeReceipted,
		Code:     spec.code,
		Rev:      rev,
		NoticeID: &noticeID,
	}, nil
}
