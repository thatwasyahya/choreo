package store

import (
	"time"

	"github.com/google/uuid"
)

// Outcomes. Every operation gets exactly one, and the set is closed — the
// column carries a CHECK constraint with the same five values.
const (
	// OutcomeApplied: the intent became a row change.
	OutcomeApplied = "applied"
	// OutcomeAbsorbed: this op_id was already seen with the same payload, so
	// the stored answer is replayed. End-to-end idempotency.
	OutcomeAbsorbed = "absorbed"
	// OutcomeReceipted: the server's value held. Nothing was lost — the
	// author's text travels in a notice with "put mine back".
	OutcomeReceipted = "receipted"
	// OutcomeSuperseded: the whole intent was overtaken by a later one.
	OutcomeSuperseded = "superseded"
	// OutcomeRefused: the intent cannot apply, and the reason says whether
	// retrying could ever help.
	OutcomeRefused = "refused"
)

// Reason codes. Localisation keys for the client, never sentences.
const (
	// ReasonFieldSuperseded: first-writer-wins kept the server's value.
	ReasonFieldSuperseded = "field_superseded"
	// ReasonTargetGone: a field write arrived for a row that does not exist. A
	// field write NEVER creates a row — otherwise a purged row resurrects
	// piecemeal and the purge destroys the evidence an arbiter needs.
	ReasonTargetGone = "target_gone"
	// ReasonForbidden: role, membership or device. Never rewritten.
	ReasonForbidden = "forbidden"
	// ReasonUnsupportedOp: this server is older than the app that sent it.
	// RETRYABLE, never terminal: with direct APK distribution that is ordinary,
	// not pathological, and a deploy-order mistake must not poison a real
	// user's outbox forever.
	ReasonUnsupportedOp = "unsupported_op"
	// ReasonMalformed: our bug, not theirs. Logged, never shown.
	ReasonMalformed = "malformed"
)

// Operation kinds the server understands. Adding one is a protocol version
// bump, not a tweak — which is why the list is here and nowhere else.
const (
	OpCreate     = "create"
	OpSetFields  = "set_fields"
	OpDelete     = "delete"
	OpRestore    = "restore"
	OpComplete   = "complete"
	OpUncomplete = "uncomplete"
)

// Entities a pull returns. Completions and tags travel INSIDE their task's
// state and have no rev of their own, so they are not listed here.
const (
	EntityTask     = "task"
	EntitySeries   = "series"
	EntityMember   = "member"
	EntityList     = "list"
	EntityListItem = "list_item"
	EntityMemo     = "memo"
	EntityNotice   = "notice"
)

// Op is one intent from a client.
//
// Intents go up, row states come down. A client never sends "here is the new
// state of the task" — it sends "rename this field, I had seen revision
// 48102". That is what lets the server arbitrate once, and what makes conflict
// detection causal rather than chronological.
type Op struct {
	OpID   uuid.UUID
	Kind   string
	Entity string
	RowID  uuid.UUID

	// BaseRevs is the revision the author actually had in front of them, per
	// field. Absent means "I did not see a value".
	BaseRevs map[string]int64

	// Fields is the intended new value per field. Names the server does not
	// know are kept in the row's ext column rather than refused, which is what
	// makes a newer app's write survive against an older server.
	Fields map[string]any

	// ClientTS is the phone's clock. Stored, never believed.
	ClientTS *time.Time

	// Raw is the verbatim payload, kept for the ops ledger and for the
	// idempotency hash.
	Raw []byte
}

// OpResult is what the server decided about one intent.
type OpResult struct {
	OpID    uuid.UUID `json:"op_id"`
	Outcome string    `json:"outcome"`
	Code    string    `json:"code,omitempty"`
	Rev     int64     `json:"rev,omitempty"`

	// Fields names the per-field decision when they differ, which is the only
	// way a client can tell which half of its edit landed.
	Fields map[string]string `json:"fields,omitempty"`

	NoticeID *uuid.UUID `json:"notice_id,omitempty"`
}

// Change is one row state travelling down.
//
// A state and not an operation log: catching up on ten days costs what changed,
// not what happened. A task edited forty times is one row. And it means there
// is no merge engine on the phone — applying a change is "replace the row if
// the incoming rev is higher", which no arrival order can break.
type Change struct {
	Entity  string    `json:"entity"`
	RowID   uuid.UUID `json:"row_id"`
	Rev     int64     `json:"rev"`
	Deleted bool      `json:"deleted"`

	// Nullable, because a notice has neither: it is addressed to a member
	// rather than edited by one. Saying so with a pointer beats inventing a
	// timestamp the client would be entitled to believe.
	UpdatedBy *uuid.UUID `json:"updated_by,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`

	State map[string]any `json:"state"`
}

// SyncInput is one round trip from one device.
type SyncInput struct {
	DeviceID   uuid.UUID
	MemberID   uuid.UUID
	AppVersion string
	Protocol   int
	CursorSeq  int64
	Limit      int
	Ops        []Op
}

// SyncOutput is the answer: one result per operation, in request order, plus
// everything that changed since the cursor.
type SyncOutput struct {
	HeadSeq         int64
	Results         []OpResult
	Changes         []Change
	CursorSeq       int64
	HasMore         bool
	PurgeHorizonSeq int64
}

// Pull bounds. A page is capped so a phone returning after ten days does not
// try to hold the household in memory at once.
const (
	defaultPullLimit = 500
	maxPullLimit     = 1000
	// maxOpsPerBatch caps how long one batch can hold the household lock.
	maxOpsPerBatch = 200
)
