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
	if op.Entity != EntityTask {
		return refusal(op.OpID, ReasonUnsupportedOp), nil
	}
	if op.RowID == uuid.Nil {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	title, ok := op.Fields["title"].(string)
	if !ok || strings.TrimSpace(title) == "" {
		// Only the title is ever mandatory (product principle 1).
		return refusal(op.OpID, ReasonMalformed), nil
	}

	kind := EntityTask
	if raw, present := op.Fields["kind"]; present {
		text, isText := raw.(string)
		if !isText || (text != "task" && text != "event") {
			return refusal(op.OpID, ReasonMalformed), nil
		}
		kind = text
	}

	// Validate before consuming a sequence number. set_fields classifies first
	// for the same reason: a malformed operation should not leave a gap in the
	// household's clock. Gaps are harmless to a cursor, but a clock that moves
	// for nothing is a clock nobody can reason about.
	known, unknown, err := split(op.Fields)
	if err != nil {
		return OpResult{}, err
	}

	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	revs := map[string]int64{}
	for name := range op.Fields {
		if name != "kind" {
			revs[name] = seq
		}
	}

	fieldRevs, err := jsonOf(revs)
	if err != nil {
		return OpResult{}, err
	}
	ext, err := jsonOf(unknown)
	if err != nil {
		return OpResult{}, err
	}

	columns := []string{"id", "household_id", "rev", "created_rev", "updated_by",
		"kind", "field_revs", "ext"}
	values := []any{op.RowID, householdID, seq, seq, in.MemberID, kind, fieldRevs, ext}
	casts := []string{"", "", "", "", "", "", "::jsonb", "::jsonb"}

	for name, value := range known {
		column, cast, _ := taskColumn(name)
		columns = append(columns, column)
		values = append(values, value)
		casts = append(casts, "::"+cast)
	}

	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = "$" + strconv.Itoa(i+1) + casts[i]
	}

	//nolint:gosec // every column name comes from taskColumn's closed switch
	query := "INSERT INTO tasks (" + strings.Join(columns, ", ") +
		") VALUES (" + strings.Join(placeholders, ", ") +
		") ON CONFLICT (household_id, id) DO NOTHING"

	tag, err := tx.Exec(ctx, query, values...)
	if err != nil {
		return OpResult{}, fmt.Errorf("store: creating a task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return OpResult{OpID: op.OpID, Outcome: OutcomeAbsorbed}, nil
	}

	return OpResult{OpID: op.OpID, Outcome: OutcomeApplied, Rev: seq}, nil
}

// split separates fields the server has columns for from those it does not.
func split(fields map[string]any) (known, unknown map[string]any, err error) {
	known = map[string]any{}
	unknown = map[string]any{}

	for name, value := range fields {
		if name == "kind" {
			continue
		}

		_, cast, ok := taskColumn(name)
		if !ok {
			unknown[name] = value
			continue
		}

		coerced, err := coerce(cast, value)
		if err != nil {
			return nil, nil, err
		}
		known[name] = coerced
	}
	return known, unknown, nil
}
