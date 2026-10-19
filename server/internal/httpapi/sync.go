package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
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
	Fields   map[string]any   `json:"fields"`
	ClientTS *time.Time       `json:"client_ts"`
}

type syncResponse struct {
	ServerNow       time.Time        `json:"server_now"`
	HeadSeq         int64            `json:"head_seq"`
	Results         []store.OpResult `json:"results"`
	Changes         []store.Change   `json:"changes"`
	CursorSeq       int64            `json:"cursor_seq"`
	HasMore         bool             `json:"has_more"`
	PurgeHorizonSeq int64            `json:"purge_horizon_seq"`

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

	out, err := h.store.ApplySync(ctx, householdID, store.SyncInput{
		DeviceID:   session.DeviceID,
		MemberID:   member.ID,
		AppVersion: req.AppVersion,
		Protocol:   req.Protocol,
		CursorSeq:  req.CursorSeq,
		Limit:      req.Limit,
		Ops:        ops,
	})
	if err != nil {
		if errors.Is(err, store.ErrBatchTooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge,
				errorBody{Error: "too_many_ops"})
			return
		}
		writeError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, syncResponse{
		ServerNow:       time.Now().UTC(),
		HeadSeq:         out.HeadSeq,
		Results:         out.Results,
		Changes:         out.Changes,
		CursorSeq:       out.CursorSeq,
		HasMore:         out.HasMore,
		PurgeHorizonSeq: out.PurgeHorizonSeq,
		ProtocolMin:     protocolMin,
		ProtocolMax:     protocolMax,
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

		ops = append(ops, store.Op{
			OpID:     opID,
			Kind:     env.Kind,
			Entity:   env.Entity,
			RowID:    rowID,
			BaseRevs: env.BaseRevs,
			Fields:   env.Fields,
			ClientTS: env.ClientTS,
			Raw:      payload,
		})
	}
	return ops, true
}
