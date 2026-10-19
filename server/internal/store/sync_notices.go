package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Choices a person can make about a notice.
const (
	ChoiceMine    = "mine"
	ChoiceTheirs  = "theirs"
	ChoiceDismiss = "dismiss"
)

// refuseAndTell records a refusal AND a card about it.
//
// Not every refusal earns one, and the line is drawn deliberately:
//
//   - `malformed` gets NO notice. It is our bug, not theirs, and the user is
//     not our monitoring. It goes to the ledger and the logs.
//   - `unsupported_op` gets NO notice. The client retries it after a deploy;
//     a card would be an apology for something that fixes itself.
//   - `target_gone` on a DELETE gets no notice either: the person wanted the
//     thing gone, and it is gone. Telling them would be noise.
//
// What does earn a card is the case where somebody did work the server could
// not record — an edit whose row has been purged, a tick on something that no
// longer exists, a step back the series will not allow. Those must never
// vanish quietly.
func (s *Store) refuseAndTell(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID,
	in SyncInput, op Op, code string, mine map[string]any,
) (OpResult, error) {
	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	if mine == nil {
		mine = map[string]any{}
	}

	noticeID, err := s.writeNotice(ctx, tx, householdID, in, op, noticeSpec{
		kind: "refusal",
		code: code,
		mine: mine,
	}, seq)
	if err != nil {
		return OpResult{}, err
	}

	return OpResult{
		OpID:     op.OpID,
		Outcome:  OutcomeRefused,
		Code:     code,
		NoticeID: &noticeID,
	}, nil
}

// opResolveNotice closes a card in the "to review" tray.
//
// An ordinary operation rather than an endpoint, which is the point: answering
// a notice works offline like everything else, queued in the outbox with the
// rest.
//
// It only CLOSES the card. Choosing "put mine back" does not re-apply anything
// here — the notice already carries the author's text, so the client replays it
// as a fresh set_fields against the current revision. That keeps this operation
// idempotent, and it keeps the re-application subject to the same arbitration
// as any other write instead of smuggling a privileged one through the back.
func (s *Store) opResolveNotice(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID, in SyncInput, op Op,
) (OpResult, error) {
	if op.RowID == uuid.Nil {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	choice, _ := op.Fields["choice"].(string)
	switch choice {
	case ChoiceMine, ChoiceTheirs, ChoiceDismiss:
	default:
		return refusal(op.OpID, ReasonMalformed), nil
	}

	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	// for_member is part of the WHERE, not a check afterwards: a decision is
	// addressed to one person, and two voices answering it is a race. Somebody
	// else's notice is simply not there to be resolved.
	const resolve = `UPDATE notices
	                    SET resolved_rev = $4, resolved_choice = $5
	                  WHERE household_id = $1 AND id = $2
	                    AND for_member = $3
	                    AND resolved_rev IS NULL`

	tag, err := tx.Exec(ctx, resolve, householdID, op.RowID, in.MemberID, seq, choice)
	if err != nil {
		return OpResult{}, fmt.Errorf("store: resolving a notice: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Already answered, or not addressed to this member. Idempotent either
		// way, and the two are deliberately indistinguishable: telling them
		// apart would say whether somebody else has an open card.
		return OpResult{OpID: op.OpID, Outcome: OutcomeAbsorbed}, nil
	}

	return OpResult{OpID: op.OpID, Outcome: OutcomeApplied, Rev: seq}, nil
}
