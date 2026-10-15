// Package obs carries the observability plumbing: structured logging, request
// correlation and Prometheus metrics.
//
// Operating rule 5 — observable before optimised. This exists from the first
// commit, not after the first incident.
package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type contextKey struct{}

// The zero value of an unexported empty struct: a context key that no other
// package can collide with.
//
//nolint:gochecknoglobals // immutable sentinel, never assigned to
var requestIDKey contextKey

// HeaderRequestID lets a proxy or a client supply its own correlation id, so a
// trace can be followed across hops.
const HeaderRequestID = "X-Request-Id"

// NewLogger builds the process logger.
//
// JSON always, including in dev: a log format that differs between machines is
// a log format nobody ends up parsing.
func NewLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// RequestID returns the correlation id carried by ctx, or an empty string.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Metrics holds the server's Prometheus collectors.
//
// It owns its registry instead of using the global default one: a package that
// registers into a hidden global cannot be tested twice in the same process.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics builds the collectors and registers them.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "choreo_http_requests_total",
			Help: "Number of HTTP requests served, by route, method and status.",
		}, []string{"route", "method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "choreo_http_request_duration_seconds",
			Help:    "How long HTTP requests take, by route and method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method"}),
	}

	m.registry.MustRegister(
		m.requests,
		m.duration,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler serves the metrics in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Instrument wraps mux with correlation, logging and metrics.
//
// It takes the mux rather than a plain http.Handler so it can ask it which
// route pattern matched. Labelling metrics with the raw path would mint a new
// time series per household id and blow up the registry.
func Instrument(logger *slog.Logger, m *Metrics, mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = newRequestID()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		r = r.WithContext(ctx)
		w.Header().Set(HeaderRequestID, id)

		_, route := mux.Handler(r)
		if route == "" {
			// Unmatched paths share one label, otherwise a scanner probing
			// random URLs would create a time series per probe.
			route = "unmatched"
		}

		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(rec, r)

		elapsed := time.Since(start)
		status := strconv.Itoa(rec.status)

		m.requests.WithLabelValues(route, r.Method, status).Inc()
		m.duration.WithLabelValues(route, r.Method).Observe(elapsed.Seconds())

		// /metrics is scraped every few seconds. Logging it at info level
		// would drown every other line.
		level := slog.LevelInfo
		switch {
		case rec.status >= http.StatusInternalServerError:
			level = slog.LevelError
		case route == "GET /metrics":
			level = slog.LevelDebug
		}

		logger.LogAttrs(ctx, level, "http request",
			slog.String("request_id", id),
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", elapsed.Milliseconds()),
		)
	})
}

// recorder remembers the status code, which net/http does not expose.
type recorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *recorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	return n, err //nolint:wrapcheck // passing through the underlying writer
}

func newRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A correlation id is never worth failing a request over.
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
