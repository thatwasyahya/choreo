// Package wake fans PostgreSQL change notifications out to parked sync calls.
//
// Why this exists at all: a client asks for changes with wait_ms, and the
// server holds the request open until something happens in that household. The
// whole value is in what the held request does NOT hold.
//
//   - ONE connection does LISTEN for the entire process. Not one per waiter:
//     a household of four with the app open would otherwise pin four
//     connections, and the pool would be the first thing to fall over.
//   - A parked request keeps a goroutine (a few kilobytes) and nothing else.
//     The handler releases its pgx connection and its transaction BEFORE
//     parking, so a parked call is never an `idle in transaction` row.
//
// That is what makes the cost ceiling of decision D-005 real rather than
// hopeful: thousands of parked calls cost memory measured in megabytes, not a
// bigger VPS.
package wake

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Channel is the PostgreSQL notification channel. The payload is a household
// id, so a notification wakes only the devices that care about it.
const Channel = "choreo_changes"

// reconnectDelay paces reattachment after the listening connection dies.
//
// Its silent death is the one failure that leaves every long poll expiring
// without a wake-up. Nothing is lost when that happens — clients fall back to
// the latency of their own retries — but it stops being invisible: the log says
// so, loudly, every time.
const reconnectDelay = 2 * time.Second

// Hub routes notifications to the goroutines waiting on them.
type Hub struct {
	// dsn, not a pool. The listening connection is held for the lifetime of
	// the process, so taking it from the pool would permanently cost the pool
	// one connection — and the pool exists to serve requests.
	dsn    string
	logger *slog.Logger

	register   chan subscription
	unregister chan subscription
	notify     chan uuid.UUID
}

type subscription struct {
	household uuid.UUID
	ready     chan struct{}
}

// New builds a hub. Run must be called for it to do anything.
func New(dsn string, logger *slog.Logger) *Hub {
	return &Hub{
		dsn:        dsn,
		logger:     logger,
		register:   make(chan subscription),
		unregister: make(chan subscription),
		notify:     make(chan uuid.UUID, 64),
	}
}

// Subscribe registers interest in one household.
//
// It returns before any wait begins, and callers MUST subscribe before reading
// the changes they are about to compare against. Subscribing afterwards leaves
// a window where a change commits between the read and the registration: the
// caller would then wait out its whole timeout for something that had already
// happened.
func (h *Hub) Subscribe(householdID uuid.UUID) (<-chan struct{}, func()) {
	sub := subscription{household: householdID, ready: make(chan struct{})}

	select {
	case h.register <- sub:
	case <-time.After(time.Second):
		// The hub is not running. Degrade to no waiting rather than blocking a
		// request: a client that polls is slower, not broken.
		closed := make(chan struct{})
		close(closed)
		return closed, func() {}
	}

	return sub.ready, func() {
		select {
		case h.unregister <- sub:
		case <-time.After(time.Second):
		}
	}
}

// Run owns the registry and the listening connection until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	go h.listen(ctx)

	waiters := map[uuid.UUID]map[chan struct{}]struct{}{}

	for {
		select {
		case <-ctx.Done():
			return

		case sub := <-h.register:
			if waiters[sub.household] == nil {
				waiters[sub.household] = map[chan struct{}]struct{}{}
			}
			waiters[sub.household][sub.ready] = struct{}{}

		case sub := <-h.unregister:
			if set := waiters[sub.household]; set != nil {
				delete(set, sub.ready)
				if len(set) == 0 {
					delete(waiters, sub.household)
				}
			}

		case household := <-h.notify:
			for ready := range waiters[household] {
				// Closing rather than sending: a waiter that has already given
				// up is not there to receive, and closing never blocks.
				close(ready)
				delete(waiters[household], ready)
			}
			if len(waiters[household]) == 0 {
				delete(waiters, household)
			}
		}
	}
}

// listen holds the single LISTEN connection and reattaches when it dies.
func (h *Hub) listen(ctx context.Context) {
	for ctx.Err() == nil {
		if err := h.listenOnce(ctx); err != nil && ctx.Err() == nil {
			h.logger.ErrorContext(ctx, "the change listener dropped",
				slog.String("channel", Channel),
				slog.String("error", err.Error()),
				slog.String("consequence",
					"long polls will expire without a wake-up until this reattaches"),
			)

			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectDelay):
			}
		}
	}
}

func (h *Hub) listenOnce(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, h.dsn)
	if err != nil {
		return err //nolint:wrapcheck // reported verbatim by the caller
	}
	defer func() { _ = conn.Close(ctx) }()

	// The channel name cannot be a parameter in LISTEN, and it is a constant
	// in this package — never anything a request can influence.
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err //nolint:wrapcheck // reported verbatim by the caller
	}

	h.logger.InfoContext(ctx, "listening for changes", slog.String("channel", Channel))

	for {
		message, err := conn.WaitForNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err //nolint:wrapcheck // reported verbatim by the caller
		}

		household, err := uuid.Parse(message.Payload)
		if err != nil {
			h.logger.WarnContext(ctx, "unparseable change notification",
				slog.String("payload", message.Payload))
			continue
		}

		select {
		case h.notify <- household:
		case <-ctx.Done():
			return nil
		default:
			// The registry is busy. Dropping a wake-up costs one client the
			// latency of its next poll; blocking here would stall every other
			// household's notifications behind it.
			h.logger.WarnContext(ctx, "dropped a change notification",
				slog.String("household_id", household.String()))
		}
	}
}
