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
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// notifyChannel is the fixed Postgres NOTIFY/LISTEN channel every
// Broadcaster instance -- across every cmd/api and cmd/ingest process --
// uses.
const notifyChannel = "kuruops_events"

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

// notifyMessage is the JSON body sent over pg_notify -- Postgres rejects a
// NOTIFY payload of 8000 bytes or more. Event's payload is meant to be small,
// but nothing about a caller's payload guarantees it, so Publish enforces the
// limit itself (see marshalNotify) rather than trusting it.
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
	// taps see every event of every tenant -- see Tap.
	taps []func(tenantID uuid.UUID, event Event)

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

// Tap registers fn to be called for every event of every tenant this process
// receives, from any process. Subscribe is per tenant and meant for one SSE
// connection each; Tap is for a process-level reaction that cares about a kind of
// event regardless of tenant (cmd/analyzer wakes its queue consumer when an alert
// is received). fn runs on the LISTEN loop's goroutine, so it must return quickly
// and never block. Call it before Start; taps are not removable.
func (b *Broadcaster) Tap(fn func(tenantID uuid.UUID, event Event)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.taps = append(b.taps, fn)
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
	data, err := b.marshalNotify(tenantID, eventType, payload)
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

// maxNotifyPayloadBytes is the budget for one NOTIFY payload. Postgres rejects
// anything at or above 8000 bytes outright -- the whole event is lost, not
// truncated -- so this leaves headroom below that for multi-byte characters
// and anything the envelope grows by.
//
// notifyMessage's doc comment used to claim "Event's own payload is always
// small", and nothing enforced it: the chat's per-turn event carried the
// model's entire reply, so a long triage report (a few KB, more once JSON
// escapes its newlines and quotes) made Postgres refuse the NOTIFY and the
// event vanished, logged only as "publish event failed". The events are a
// "something changed, go and reload" nudge -- clients refetch the real data
// over REST -- so an oversized one degrades to its small identifying fields
// instead of being lost.
const maxNotifyPayloadBytes = 7000

// marshalNotify builds the NOTIFY payload for an event, shrinking the event's
// own payload if the whole message would not fit. In order of preference:
//
//  1. fits as given: unchanged;
//  2. an object whose largest fields are dropped until it fits, with
//     "truncated": true added so a consumer can tell it was cut -- the small
//     identifying fields (ids, action) survive, which is all a nudge needs;
//  3. anything else (a huge string, or an object whose every field is huge):
//     no payload at all. The event type still arrives.
func (b *Broadcaster) marshalNotify(tenantID uuid.UUID, eventType string, payload any) ([]byte, error) {
	data, err := json.Marshal(notifyMessage{TenantID: tenantID, Event: Event{Type: eventType, Payload: payload}})
	if err != nil {
		return nil, err
	}
	if len(data) <= maxNotifyPayloadBytes {
		return data, nil
	}

	shrunk, dropped := shrinkPayload(payload, maxNotifyPayloadBytes-envelopeOverhead(tenantID, eventType))
	b.logger.Warn("event payload exceeds the NOTIFY limit; delivering a reduced payload instead of losing the event",
		"type", eventType, "original_bytes", len(data), "dropped_fields", dropped, "kept_payload", shrunk != nil)
	return json.Marshal(notifyMessage{TenantID: tenantID, Event: Event{Type: eventType, Payload: shrunk}})
}

// envelopeOverhead is how many bytes the message takes with no payload at all,
// plus room for the "truncated" marker shrinkPayload may add.
func envelopeOverhead(tenantID uuid.UUID, eventType string) int {
	empty, _ := json.Marshal(notifyMessage{TenantID: tenantID, Event: Event{Type: eventType}})
	return len(empty) + len(`,"payload":{"truncated":true}`)
}

// shrinkPayload returns payload reduced to at most budget bytes of JSON, and
// the names of the fields it had to drop. A nil result means nothing usable
// was left.
func shrinkPayload(payload any, budget int) (map[string]any, []string) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, nil // not an object: a string, number, array...
	}

	// Drop the largest field first, repeatedly, so the most small identifying
	// fields survive.
	names := make([]string, 0, len(fields))
	size := 2 // braces
	for k, v := range fields {
		names = append(names, k)
		size += len(k) + len(v) + 4 // quotes, colon, comma
	}
	sort.Slice(names, func(i, j int) bool { return len(fields[names[i]]) > len(fields[names[j]]) })

	var dropped []string
	for _, k := range names {
		if size <= budget {
			break
		}
		size -= len(k) + len(fields[k]) + 4
		dropped = append(dropped, k)
		delete(fields, k)
	}
	if size > budget || len(fields) == 0 {
		return nil, dropped
	}

	out := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			continue
		}
		out[k] = decoded
	}
	out["truncated"] = true
	return out, dropped
}

// deliverLocally is what Publish used to do directly before it went through
// Postgres NOTIFY: fan out to every subscriber currently connected on THIS
// process for tenantID. Non-blocking, same reasoning as before -- a
// slow/stuck subscriber's full channel drops the event for that one
// connection rather than stalling delivery to everyone else.
func (b *Broadcaster) deliverLocally(tenantID uuid.UUID, event Event) {
	b.mu.Lock()
	for ch := range b.subs[tenantID] {
		select {
		case ch <- event:
		default:
		}
	}
	taps := b.taps
	b.mu.Unlock()
	for _, fn := range taps {
		fn(tenantID, event)
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
