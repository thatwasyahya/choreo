package httpapi

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/obs"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// snapshot streams the whole live state of a household as NDJSON.
//
// The stream carries an explicit end line. That matters more than it looks: once
// the first byte is out the status code is already 200, so a failure halfway
// through can only truncate the body. A client that reads no "end" knows it has
// a partial world and must not apply it — which is the only way a truncated
// snapshot cannot be mistaken for a closed one.
func (h *handlers) snapshot(
	w http.ResponseWriter, r *http.Request, householdID uuid.UUID, _ store.Member,
) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")

	var out io.Writer = w
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")

		compressed := gzip.NewWriter(w)
		defer func() { _ = compressed.Close() }()
		out = compressed
	}

	encoder := json.NewEncoder(out)

	rows, err := h.store.Snapshot(r.Context(), householdID,
		func(header store.SnapshotHeader) error {
			return encoder.Encode(map[string]any{
				"type":              "header",
				"cursor_seq":        header.CursorSeq,
				"purge_horizon_seq": header.PurgeHorizonSeq,
				"tz":                header.TZ,
				"protocol":          protocolMax,
			})
		},
		func(row store.SnapshotRow) error {
			return encoder.Encode(map[string]any{
				"type":   row.Entity,
				"row_id": row.RowID,
				"rev":    row.Rev,
				"state":  row.State,
			})
		},
	)
	if err != nil {
		// Nothing useful can be said to the client at this point if the stream
		// has started: the status is long gone. The missing end line is the
		// signal, and this is for us.
		h.logger.ErrorContext(r.Context(), "snapshot failed mid-stream",
			slog.String("request_id", obs.RequestID(r.Context())),
			slog.String("household_id", householdID.String()),
			slog.Int("rows_sent", rows),
			slog.String("error", err.Error()),
		)
		return
	}

	if err := encoder.Encode(map[string]any{"type": "end", "rows": rows}); err != nil {
		h.logger.ErrorContext(r.Context(), "could not close a snapshot stream",
			slog.String("request_id", obs.RequestID(r.Context())),
			slog.String("error", err.Error()),
		)
	}
}
