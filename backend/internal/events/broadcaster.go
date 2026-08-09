// Package events is a pub/sub used to push live alert/incident
// notifications to connected browser tabs over Server-Sent Events (see
// httpserver/handlers.EventsHandlers.Stream). Publish sends a Postgres
// NOTIFY on a fixed channel instead of only updating an in-process map, and
// a dedicated LISTEN connection (started via Start) feeds every event --
// including this same process's own -- back into the local fan-out this
// package always had. That's what makes it safe with more than one cmd/api
// replica: every replica's LISTEN connection sees every NOTIFY regardless
// of which process issued it, so a client connected to replica A gets an
// event published on replica B. It's also what lets cmd/ingest publish at
// all -- previously it had no Broadcaster wired in and alerts it created
// never reached any connected SSE client, even with a single api replica.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// notifyChannel is the fixed Postgres NOTIFY/LISTEN channel every
// Broadcaster instance -- across every cmd/api and cmd/ingest process --
// uses.
const notifyChannel = "argusops_events"

// reconnectDelay is how long Start waits before retrying the LISTEN
// connection after it drops (network blip, Postgres restart, etc.).
const reconnectDelay = 2 * time.Second

// Event is a small, JSON-serializable notification pushed to a tenant's
// connected SSE clients. Type is a short discriminator ("alert",
// "incident") the frontend uses to decide what to reload; Payload carries
// just enough detail to be useful without requiring the client to know the
// full domain shape -- clients still fetch the authoritative data via the
// normal REST endpoints, this is only a "something changed" nudge.
type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// notifyMessage is the JSON body sent over pg_notify -- Postgres caps a
// NOTIFY payload at 8000 bytes, comfortably enough since Event's own
// payload is always small (see its doc comment).
type notifyMessage struct {
	TenantID uuid.UUID `json:"tenantId"`
	Event    Event     `json:"event"`
}

// Broadcaster fans a tenant's events out to every SSE connection currently
// subscribed for that tenant, across every process (see the package doc).
type Broadcaster struct {
	pool   *pgxpool.Pool
	logger *slog.Logger

	mu   sync.Mutex
	subs map[uuid.UUID]map[chan Event]struct{}

	readyOnce sync.Once
	ready     chan struct{}
}

// NewBroadcaster does not open the dedicated LISTEN connection itself --
// call Start once the caller's own long-lived context (the one cancelled on
// shutdown) is available. pool is used only to send NOTIFY (see Publish);
// the LISTEN side always opens its own standalone connection (see Start),
// deliberately never borrowed from pool, since a connection with an active
// LISTEN must never be handed back to a pool for unrelated use.
func NewBroadcaster(pool *pgxpool.Pool, logger *slog.Logger) *Broadcaster {
	return &Broadcaster{
		pool:   pool,
		logger: logger,
		subs:   make(map[uuid.UUID]map[chan Event]struct{}),
		ready:  make(chan struct{}),
	}
}

// WaitReady blocks until the LISTEN connection has been established at
// least once, or ctx is done first -- events published before that point
// can be missed (same as a real NOTIFY sent before any client is
// listening), so anything that depends on delivery actually working --
// tests, in particular -- should wait for this before calling Publish.
func (b *Broadcaster) WaitReady(ctx context.Context) error {
	select {
	case <-b.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// subscriberBuffer bounds how many unread events a slow SSE client can
// accumulate before local delivery starts dropping (not blocking) further
// ones for that connection.
const subscriberBuffer = 16

// Subscribe registers a new listener for tenantID's events. The caller must
// call the returned unsubscribe func exactly once (typically via defer)
// when done, or the channel and its map entry leak.
func (b *Broadcaster) Subscribe(tenantID uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)

	b.mu.Lock()
	if b.subs[tenantID] == nil {
		b.subs[tenantID] = make(map[chan Event]struct{})
	}
	b.subs[tenantID][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs[tenantID], ch)
			if len(b.subs[tenantID]) == 0 {
				delete(b.subs, tenantID)
			}
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, unsubscribe
}

// Publish sends tenantID's event as a Postgres NOTIFY on notifyChannel --
// every process with a running Start loop (including this one) picks it up
// via deliverLocally and fans it out to its own connected SSE clients. Best
// effort: a failure here is logged, not returned, matching this package's
// existing "SSE is a live-UI nudge, not guaranteed delivery" contract (see
// deliverLocally). Uses context.Background() rather than accepting a ctx
// parameter -- Publish's signature is a fixed callback shape
// (service.EnableEventPublishing), always called from deep inside a
// request/service call where the caller's own context may already be
// gone by the time this NOTIFY actually needs to go out.
func (b *Broadcaster) Publish(tenantID uuid.UUID, eventType string, payload any) {
	data, err := json.Marshal(notifyMessage{TenantID: tenantID, Event: Event{Type: eventType, Payload: payload}})
	if err != nil {
		b.logger.Error("marshal event failed", "error", err)
		return
	}
	// pg_notify() as a function call (not the `NOTIFY channel, payload` SQL
	// statement) accepts both arguments as regular bind parameters, so the
	// JSON payload never needs manual quoting/escaping.
	if _, err := b.pool.Exec(context.Background(), "select pg_notify($1, $2)", notifyChannel, string(data)); err != nil {
		b.logger.Error("publish event failed", "error", err)
	}
}

// deliverLocally is what Publish used to do directly before it went through
// Postgres NOTIFY: fan out to every subscriber currently connected on THIS
// process for tenantID. Non-blocking, same reasoning as before -- a
// slow/stuck subscriber's full channel drops the event for that one
// connection rather than stalling delivery to everyone else.
func (b *Broadcaster) deliverLocally(tenantID uuid.UUID, event Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[tenantID] {
		select {
		case ch <- event:
		default:
		}
	}
}

// Start opens a dedicated LISTEN connection and runs until ctx is
// cancelled, feeding every notification -- from any process, including this
// one's own Publish calls -- into deliverLocally. Call it once per process,
// after constructing the Broadcaster; it blocks the calling goroutine, so
// callers run it via `go broadcaster.Start(ctx)`, same as this app's HTTP
// servers. Connection drops (Postgres restart, network blip) are logged and
// retried after reconnectDelay until ctx is cancelled -- SSE clients just
// see a gap in live updates during that window, same "best effort" contract
// as Publish.
func (b *Broadcaster) Start(ctx context.Context) {
	for ctx.Err() == nil {
		if err := b.listenOnce(ctx); err != nil && ctx.Err() == nil {
			b.logger.Error("event listener connection lost", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

// listenOnce owns one LISTEN connection's whole lifecycle: connect, LISTEN,
// then read notifications until ctx is cancelled or the connection drops.
func (b *Broadcaster) listenOnce(ctx context.Context) error {
	connConfig := b.pool.Config().ConnConfig.Copy()
	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.Background()) //nolint:errcheck // best effort on an already-broken/closing connection

	if _, err := conn.Exec(ctx, "listen "+notifyChannel); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	b.logger.Info("event listener connected")
	b.readyOnce.Do(func() { close(b.ready) })

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait for notification: %w", err)
		}

		var msg notifyMessage
		if err := json.Unmarshal([]byte(notification.Payload), &msg); err != nil {
			b.logger.Error("unmarshal event notification failed", "error", err)
			continue
		}
		b.deliverLocally(msg.TenantID, msg.Event)
	}
}
