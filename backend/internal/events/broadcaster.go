// Package events is an in-process pub/sub used to push live alert/incident
// notifications to connected browser tabs over Server-Sent Events (see
// httpserver/handlers.EventsHandlers.Stream). Purely in-memory, scoped to
// one cmd/api process -- fine for this app's single-instance deployment
// story (see the same reasoning already applied to
// middleware.NewRateLimiter and secrets.EnvStore), but a second api
// replica would each only see the events published on its own process, so
// a client connected to replica A would miss an event published on
// replica B. A Redis/NATS-backed Broadcaster would be the fix if this ever
// needs to scale horizontally -- not attempted here.
package events

import (
	"sync"

	"github.com/google/uuid"
)

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

// Broadcaster fans a tenant's events out to every SSE connection currently
// subscribed for that tenant.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan Event]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[uuid.UUID]map[chan Event]struct{})}
}

// subscriberBuffer bounds how many unread events a slow SSE client can
// accumulate before Publish starts dropping (not blocking -- see Publish)
// further ones for that connection.
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

// Publish fans an event out to every subscriber currently connected for
// tenantID. Non-blocking: a slow/stuck subscriber's full channel drops the
// event for that one connection rather than stalling the publisher (an
// AlertService/IncidentService call, on the request path) -- SSE here is a
// best-effort live-UI nudge, not a guaranteed-delivery queue.
func (b *Broadcaster) Publish(tenantID uuid.UUID, eventType string, payload any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[tenantID] {
		select {
		case ch <- Event{Type: eventType, Payload: payload}:
		default:
		}
	}
}
