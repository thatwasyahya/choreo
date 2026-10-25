package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ApplySync is the protocol: push intents and pull row states in one round trip.
//
// Each operation runs in its OWN transaction, not one per batch. A refused
// operation must not roll back the eleven good ones in front of it, and that is
// what matters on the day a phone comes back after ten days with a full outbox.
func (s *Store) ApplySync(
	ctx context.Context, householdID uuid.UUID, in SyncInput,
) (SyncOutput, error) {
	if len(in.Ops) > maxOpsPerBatch {
		return SyncOutput{}, ErrBatchTooLarge
	}

	results := make([]OpResult, 0, len(in.Ops))
	for _, op := range in.Ops {
		result, err := s.applyOne(ctx, householdID, in, op)
		if err != nil {
			return SyncOutput{}, err
		}
		results = append(results, result)
	}

	out, err := s.pull(ctx, householdID, in.CursorSeq, in.Limit)
	if err != nil {
		return SyncOutput{}, err
	}
	out.Results = results
	return out, nil
}

// applyOne arbitrates a single intent, once, under the household lock.
func (s *Store) applyOne(
	ctx context.Context, householdID uuid.UUID, in SyncInput, op Op,
) (OpResult, error) {
	digest := sha256.Sum256(op.Raw)

	var result OpResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		prior, found, err := s.priorOp(ctx, tx, householdID, op.OpID)
		if err != nil {
			return err
		}
		if found {
			// Same op_id. Either the same payload — an honest retry — or a
			// different one, which is a client bug we must not paper over.
			if prior.hash == digest {
				result = prior.result
				// The stored answer, with the outcome telling the client this
				// was a replay rather than a fresh application.
				result.Outcome = OutcomeAbsorbed
				return nil
			}
			// Without this check a mutation disappears with no trace at all:
			// the client would receive the OTHER operation's answer and tick
			// its outbox entry off as done.
			result = refusal(op.OpID, ReasonMalformed)
			return nil
		}

		switch op.Kind {
		case OpCreate:
			result, err = s.opCreate(ctx, tx, householdID, in, op)
		case OpSetFields:
			result, err = s.opSetFields(ctx, tx, householdID, in, op)
		case OpDelete:
			result, err = s.opDelete(ctx, tx, householdID, in, op)
		case OpRestore:
			result, err = s.opRestore(ctx, tx, householdID, in, op)
		case OpComplete:
			result, err = s.opComplete(ctx, tx, householdID, in, op)
		case OpUncomplete:
			result, err = s.opUncomplete(ctx, tx, householdID, in, op)
		case OpResolveNotice:
			result, err = s.opResolveNotice(ctx, tx, householdID, in, op)
		default:
			// Retryable on purpose. A server older than the app is ordinary
			// with direct APK distribution, and a deploy-order mistake must not
			// poison a real user's outbox forever.
			result = refusal(op.OpID, ReasonUnsupportedOp)
		}
		if err != nil {
			if errors.Is(err, errFieldType) {
				// Our bug, not theirs: logged through the ledger, never shown.
				result = refusal(op.OpID, ReasonMalformed)
			} else {
				return err
			}
		}

		return s.recordOp(ctx, tx, householdID, in, op, digest, result)
	})
	if err != nil {
		return OpResult{}, err
	}
	return result, nil
}

func refusal(opID uuid.UUID, reason string) OpResult {
	return OpResult{OpID: opID, Outcome: OutcomeRefused, Code: reason}
}

type storedOp struct {
	hash   [32]byte
	result OpResult
}

func (s *Store) priorOp(
	ctx context.Context, tx pgx.Tx, householdID, opID uuid.UUID,
) (storedOp, bool, error) {
	const q = `SELECT payload_hash, result FROM ops
	            WHERE household_id = $1 AND op_id = $2`

	var (
		hash   []byte
		result []byte
	)
	err := tx.QueryRow(ctx, q, householdID, opID).Scan(&hash, &result)
	switch {
	case errorsIsNoRows(err):
		return storedOp{}, false, nil
	case err != nil:
		return storedOp{}, false, fmt.Errorf("store: reading the op ledger: %w", err)
	}

	var stored storedOp
	copy(stored.hash[:], hash)
	if len(result) > 0 {
		if err := json.Unmarshal(result, &stored.result); err != nil {
			return storedOp{}, false, fmt.Errorf("store: decoding a stored result: %w", err)
		}
	}
	return stored, true, nil
}

// recordOp writes the ledger entry, in the same transaction as the effect.
//
// Idempotency has to be atomic with what it describes: a ledger row without its
// row change, or the reverse, and a retry does the wrong thing.
func (s *Store) recordOp(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID,
	in SyncInput, op Op, digest [32]byte, result OpResult,
) error {
	encoded, err := jsonOf(result)
	if err != nil {
		return err
	}

	payload := op.Raw
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}

	const q = `INSERT INTO ops
	    (household_id, op_id, payload_hash, device_id, member_id, kind, row_id,
	     payload, outcome, reason, result_rev, result, client_ts,
	     app_version, protocol)
	    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`

	var resultRev *int64
	if result.Rev != 0 {
		resultRev = &result.Rev
	}
	var reason *string
	if result.Code != "" {
		reason = &result.Code
	}

	if _, err := tx.Exec(ctx, q,
		householdID, op.OpID, digest[:], in.DeviceID, in.MemberID, op.Kind, nullUUID(op.RowID),
		payload, result.Outcome, reason, resultRev, encoded, op.ClientTS,
		in.AppVersion, in.Protocol,
	); err != nil {
		return fmt.Errorf("store: recording an op: %w", err)
	}
	return nil
}

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// opCreate inserts a row whose id was minted by the client.
//
// ON CONFLICT DO NOTHING rather than an error: two phones that both create the
// same row — the same derived occurrence, say — are not in conflict, they agree.
// The second one is absorbed.
func (s *Store) opCreate(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID, in SyncInput, op Op,
) (OpResult, error) {
	// A recurring chore is created the same way as anything else — one
	// operation, queued offline like the rest — but it writes a different
	// table and has to open its first occurrence, so it takes its own path.
	if op.Entity == EntitySeries {
		if in.Protocol < protocolSeries {
			// An older client cannot have sent this, so it is a bug rather
			// than a feature negotiation. Retryable all the same: the next
			// deploy may be what fixes it.
			return refusal(op.OpID, ReasonUnsupportedOp), nil
		}
		return s.opCreateSeries(ctx, tx, householdID, in, op)
	}

	shape, exists := shapeOf(op.Entity)
	if !exists || in.Protocol < shape.since {
		return refusal(op.OpID, ReasonUnsupportedOp), nil
	}
	if op.RowID == uuid.Nil {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	// Exactly one field is mandatory, and it is one for every entity: a title,
	// a name, a label. Capturing something costs a single line of typing
	// (product principle 1); everything else is filled in later or never.
	text, isText := op.Fields[shape.mandatory].(string)
	if !isText || strings.TrimSpace(text) == "" {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	kind, ok := kindOf(shape, op.Fields)
	if !ok {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	parent, wrong, err := s.liveParent(ctx, tx, shape, householdID, op.Fields)
	if err != nil {
		return OpResult{}, err
	}
	switch wrong {
	case ReasonMalformed:
		return refusal(op.OpID, ReasonMalformed), nil
	case ReasonTargetGone:
		// The author's words have to come back to them: somebody typed
		// "butter" on the way to the shop and the list had been deleted an
		// hour before. The same rule as a field write against a purged row.
		return s.refuseAndTell(
			ctx, tx, householdID, in, op, ReasonTargetGone, op.Fields)
	}

	// Validate before consuming a sequence number. set_fields classifies first
	// for the same reason: a malformed operation should not leave a gap in the
	// household clock. Gaps are harmless to a cursor, but a clock that moves
	// for nothing is a clock nobody can reason about.
	known, unknown, err := split(shape, op.Fields)
	if err != nil {
		return OpResult{}, err
	}

	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	revs := map[string]int64{}
	for name := range op.Fields {
		if writtenOnce(shape, name) {
			continue
		}
		revs[name] = seq
	}

	fieldRevs, err := jsonOf(revs)
	if err != nil {
		return OpResult{}, err
	}
	ext, err := jsonOf(unknown)
	if err != nil {
		return OpResult{}, err
	}

	query, values := insertStatement(shape, newRow{
		id: op.RowID, house: householdID, by: in.MemberID, seq: seq,
		kind: kind, parent: parent,
		known: known, fieldRevs: fieldRevs, ext: ext,
	})

	tag, err := tx.Exec(ctx, query, values...)
	if err != nil {
		return OpResult{}, fmt.Errorf("store: creating a %s: %w", shape.entity, err)
	}
	if tag.RowsAffected() == 0 {
		return OpResult{OpID: op.OpID, Outcome: OutcomeAbsorbed}, nil
	}

	return OpResult{OpID: op.OpID, Outcome: OutcomeApplied, Rev: seq}, nil
}

// kindOf reads the kind a new row is born with.
//
// Set once and never written again, so this is the only place that decides it.
// A table with no kind column answers with the empty string, which the insert
// then leaves out.
func kindOf(shape rowShape, fields map[string]any) (string, bool) {
	raw, present := fields["kind"]
	if !present || len(shape.kinds) == 0 {
		return shape.defaultKind(), true
	}

	named, isText := raw.(string)
	if !isText || !shape.accepts(named) {
		return "", false
	}
	return named, true
}

// liveParent resolves the row this one hangs off, where it hangs off anything.
//
// An item belongs to a list, and a list that is gone cannot hold one. The
// foreign key would catch that too, but as an aborted transaction and a 500 —
// our problem, and unanswerable. Asked here, it is a refusal with a reason.
//
// The second return is empty when nothing is wrong, and otherwise the refusal
// the caller should give.
func (s *Store) liveParent(
	ctx context.Context, tx pgx.Tx, shape rowShape,
	householdID uuid.UUID, fields map[string]any,
) (*uuid.UUID, string, error) {
	if shape.parent == "" {
		return nil, "", nil
	}

	id, ok := uuidField(fields[shape.parent])
	if !ok {
		return nil, ReasonMalformed, nil
	}

	alive, err := s.rowAlive(ctx, tx, shape.parentIn, householdID, id)
	if err != nil {
		return nil, "", err
	}
	if !alive {
		return nil, ReasonTargetGone, nil
	}
	return &id, "", nil
}

// newRow is everything a creation writes, assembled before any SQL exists.
type newRow struct {
	id     uuid.UUID
	house  uuid.UUID
	by     uuid.UUID
	seq    int64
	kind   string
	parent *uuid.UUID

	known     map[string]any
	fieldRevs []byte
	ext       []byte
}

// insertStatement assembles the INSERT for one new row.
//
// Pure, and kept apart from running it, because the whole interest is in which
// columns a table actually has: a list has a kind and no parent, an item has a
// parent and no kind, a task has a kind and no parent, and every one of them
// has an id, a revision and somewhere to keep the fields we do not know.
func insertStatement(shape rowShape, row newRow) (string, []any) {
	columns := []string{"id", "household_id", "rev", "created_rev", "updated_by",
		"field_revs", "ext"}
	values := []any{row.id, row.house, row.seq, row.seq, row.by, row.fieldRevs, row.ext}
	casts := []string{"", "", "", "", "", "::jsonb", "::jsonb"}

	if len(shape.kinds) > 0 {
		columns = append(columns, "kind")
		values = append(values, row.kind)
		casts = append(casts, "")
	}
	if row.parent != nil {
		columns = append(columns, shape.parent)
		values = append(values, *row.parent)
		casts = append(casts, "")
	}

	for name, value := range row.known {
		col, _ := shape.column(name)
		columns = append(columns, col.name)
		values = append(values, value)
		casts = append(casts, "::"+col.cast)
	}

	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = "$" + strconv.Itoa(i+1) + casts[i]
	}

	//nolint:gosec // every name comes from shapeOf and its closed column switch
	query := "INSERT INTO " + shape.table + " (" + strings.Join(columns, ", ") +
		") VALUES (" + strings.Join(placeholders, ", ") +
		") ON CONFLICT (household_id, id) DO NOTHING"

	return query, values
}

// writtenOnce reports whether a field is part of the row identity rather than
// its content: set by the creation and never writable again.
//
// Neither belongs in field_revs or in ext. A second, stale copy of a row own
// identity sitting in its ext column is the kind of thing that reads as data
// for a year and then decides an argument.
func writtenOnce(shape rowShape, field string) bool {
	return field == "kind" || (shape.parent != "" && field == shape.parent)
}

// split separates fields the server has columns for from those it does not.
func split(shape rowShape, fields map[string]any) (known, unknown map[string]any, err error) {
	known = map[string]any{}
	unknown = map[string]any{}

	for name, value := range fields {
		if writtenOnce(shape, name) {
			continue
		}

		col, ok := shape.column(name)
		if !ok {
			unknown[name] = value
			continue
		}

		coerced, err := coerce(col, value)
		if err != nil {
			return nil, nil, err
		}
		known[name] = coerced
	}
	return known, unknown, nil
}

// uuidField reads an id that arrived as JSON.
func uuidField(raw any) (uuid.UUID, bool) {
	text, ok := raw.(string)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(text)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// rowAlive reports whether a row exists and is not buried.
//
// FOR SHARE, not a bare read: it has to still be true when the insert lands.
// The lock order is the same as every lifecycle operation — the row first, the
// household sequence second — so nothing here can deadlock against a deletion.
func (s *Store) rowAlive(
	ctx context.Context, tx pgx.Tx, table string, householdID, rowID uuid.UUID,
) (bool, error) {
	//nolint:gosec // table comes from shapeOf closed switch, never from the wire
	q := "SELECT deleted_rev IS NULL FROM " + table +
		" WHERE household_id = $1 AND id = $2 FOR SHARE"

	var alive bool
	err := tx.QueryRow(ctx, q, householdID, rowID).Scan(&alive)
	switch {
	case errorsIsNoRows(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("store: checking a parent row: %w", err)
	}
	return alive, nil
}
