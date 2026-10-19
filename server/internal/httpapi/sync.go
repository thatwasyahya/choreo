package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

type syncRequest struct {
	Protocol   int               `json:"protocol"`
	DeviceID   string            `json:"device_id"`
	AppVersion string            `json:"app_version"`
	CursorSeq  int64             `json:"cursor_seq"`
	Limit      int               `json:"limit"`
	WaitMS     int               `json:"wait_ms"`
	OutboxAgeS int               `json:"outbox_age_s"`
	ClientNow  *time.Time        `json:"client_now"`
	Ops        []json.RawMessage `json:"ops"`
}

// opEnvelope is the wire form of one intent.
//
// The raw bytes are kept alongside: the idempotency hash is taken over exactly
// what the client sent, so a retry of the same outbox entry hashes the same
// without anyone having to agree on a canonical encoding.
type opEnvelope struct {
	OpID     string           `json:"op_id"`
	Kind     string           `json:"kind"`
	Entity   string           `json:"entity"`
	RowID    string           `json:"row_id"`
	BaseRevs map[string]int64 `json:"base_revs"`
	BaseRev  *int64           `json:"base_rev"`
	Fields   map[string]any   `json:"fields"`

	CompletionID string   `json:"completion_id"`
	OccurredOn   string   `json:"occurred_on"`
	Observed     []string `json:"observed"`

	ClientTS *time.Time `json:"client_ts"`
}

type syncResponse struct {
	ServerNow       time.Time        `json:"server_now"`
	HeadSeq         int64            `json:"head_seq"`
	Results         []store.OpResult `json:"results"`
	Changes         []store.Change   `json:"changes"`
	CursorSeq       int64            `json:"cursor_seq"`
	HasMore         bool             `json:"has_more"`
	PurgeHorizonSeq int64            `json:"purge_horizon_seq"`

	// SnapshotRequired tells the device to rebuild from GET /snapshot. It
	// travels in a 200 and not a 410 so the operations just pushed are still
	// applied and still answered: "your cursor expired" must never cost
	// somebody their offline edits.
	SnapshotRequired bool `json:"snapshot_required"`

	// Version negotiation. A phone takes weeks to update, so the server says
	// what it can still serve rather than letting an old client discover it by
	// failing.
	ProtocolMin int `json:"protocol_min"`
	ProtocolMax int `json:"protocol_max"`
}

// Protocol versions this server speaks.
const (
	protocolMin = 1
	protocolMax = 1
)

// maxWait caps how long one request may be held open. The protocol asks for
// twenty seconds; the cap exists so a client cannot pin a goroutine for an hour
// by asking nicely.
const maxWait = 30 * time.Second

func waitFor(milliseconds int) time.Duration {
	if milliseconds <= 0 {
		return 0
	}
	if requested := time.Duration(milliseconds) * time.Millisecond; requested < maxWait {
		return requested
	}
	return maxWait
}

// sync is the protocol: push intents, pull row states, one round trip.
//
// A tap on "done" is one HTTP call that sends the gesture AND brings back what
// the other phone added.
func (h *handlers) sync(
	w http.ResponseWriter, r *http.Request, householdID uuid.UUID, member store.Member,
) {
	var req syncRequest
	if !decode(w, r, &req) {
		return
	}

	if req.Protocol < protocolMin || req.Protocol > protocolMax {
		writeJSON(w, http.StatusUpgradeRequired, errorBody{Error: "protocol_unsupported"})
		return
	}

	ctx := r.Context()
	session := caller(ctx)

	// The body carries device_id for logs and for grep, but authority comes
	// from the token. A mismatch is a client bug, and letting it through would
	// let one installation advance another's cursor.
	if req.DeviceID != "" && req.DeviceID != session.DeviceID.String() {
		writeJSON(w, http.StatusForbidden, errorBody{Error: codeForbiddenRole})
		return
	}

	ops, ok := parseOps(w, req.Ops)
	if !ok {
		return
	}

	wait := waitFor(req.WaitMS)

	// Subscribe BEFORE reading anything. A change that commits between the read
	// and the registration would otherwise be missed, and the client would hold
	// its request open for the full timeout waiting for something that had
	// already happened.
	var (
		woken   <-chan struct{}
		release = func() {}
	)
	if wait > 0 && h.wake != nil {
		woken, release = h.wake.Subscribe(householdID)
	}
	defer release()

	input := store.SyncInput{
		DeviceID:   session.DeviceID,
		MemberID:   member.ID,
		AppVersion: req.AppVersion,
		Protocol:   req.Protocol,
		CursorSeq:  req.CursorSeq,
		Limit:      req.Limit,
		Ops:        ops,
	}

	out, err := h.store.ApplySync(ctx, householdID, input)
	if err != nil {
		if errors.Is(err, store.ErrBatchTooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge,
				errorBody{Error: "too_many_ops"})
			return
		}
		writeError(w, r, h.logger, err)
		return
	}

	// Park only when there is genuinely nothing to say. A client that pushed
	// operations is owed its results now.
	if woken != nil && len(out.Changes) == 0 && len(out.Results) == 0 {
		// Nothing of the database is held here. ApplySync has returned, so its
		// connection and its transaction are back in the pool: what waits is a
		// goroutine, which is the whole point of doing it this way.
		timer := time.NewTimer(wait)
		defer timer.Stop()

		select {
		case <-woken:
			input.Ops = nil
			if out, err = h.store.ApplySync(ctx, householdID, input); err != nil {
				writeError(w, r, h.logger, err)
				return
			}

		case <-timer.C:
			// Nothing happened. The head goes in a header so the client can
			// tell "nothing new" from "I am behind" without a body.
			w.Header().Set("Choreo-Head-Seq", strconv.FormatInt(out.HeadSeq, 10))
			w.WriteHeader(http.StatusNoContent)
			return

		case <-ctx.Done():
			// The client hung up. Nothing to answer and nothing to clean up
			// beyond the deferred release.
			return
		}
	}

	writeJSON(w, http.StatusOK, syncResponse{
		ServerNow:        time.Now().UTC(),
		HeadSeq:          out.HeadSeq,
		Results:          out.Results,
		Changes:          out.Changes,
		CursorSeq:        out.CursorSeq,
		HasMore:          out.HasMore,
		PurgeHorizonSeq:  out.PurgeHorizonSeq,
		SnapshotRequired: out.SnapshotRequired,
		ProtocolMin:      protocolMin,
		ProtocolMax:      protocolMax,
	})
}

// parseOps turns the wire form into intents, keeping the raw bytes.
//
// A malformed envelope fails the whole request rather than becoming a refused
// operation: at that point we cannot even read the op_id, so there is nothing
// to answer about and nothing to make idempotent.
func parseOps(w http.ResponseWriter, raw []json.RawMessage) ([]store.Op, bool) {
	ops := make([]store.Op, 0, len(raw))

	for _, payload := range raw {
		var env opEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: codeMalformed})
			return nil, false
		}

		opID, err := uuid.Parse(env.OpID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: codeMalformed})
			return nil, false
		}

		// row_id may legitimately be absent on an operation that does not
		// target a row yet.
		var rowID uuid.UUID
		if env.RowID != "" {
			if rowID, err = uuid.Parse(env.RowID); err != nil {
				writeJSON(w, http.StatusBadRequest, errorBody{Error: codeMalformed})
				return nil, false
			}
		}

		var completionID uuid.UUID
		if env.CompletionID != "" {
			if completionID, err = uuid.Parse(env.CompletionID); err != nil {
				writeJSON(w, http.StatusBadRequest, errorBody{Error: codeMalformed})
				return nil, false
			}
		}

		observed := make([]uuid.UUID, 0, len(env.Observed))
		for _, raw := range env.Observed {
			member, err := uuid.Parse(raw)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, errorBody{Error: codeMalformed})
				return nil, false
			}
			observed = append(observed, member)
		}

		ops = append(ops, store.Op{
			OpID:         opID,
			Kind:         env.Kind,
			Entity:       env.Entity,
			RowID:        rowID,
			BaseRevs:     env.BaseRevs,
			BaseRev:      env.BaseRev,
			Fields:       env.Fields,
			CompletionID: completionID,
			OccurredOn:   env.OccurredOn,
			Observed:     observed,
			ClientTS:     env.ClientTS,
			Raw:          payload,
		})
	}
	return ops, true
}
