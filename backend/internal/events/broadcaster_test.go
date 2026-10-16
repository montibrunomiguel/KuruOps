package events_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/events"
	"github.com/kuruops/kuruops/internal/testutil"
)

// newTestBroadcaster builds a Broadcaster against a real test database
// (Publish now sends a Postgres NOTIFY, and delivery only happens once a
// LISTEN connection picks it back up -- see Start), starts its listener,
// and waits for that listener to actually be connected before handing it
// back. Without that wait, a Publish call racing the not-yet-established
// LISTEN connection would be silently missed, the same way a real NOTIFY
// sent before any client is listening is never queued by Postgres.
func newTestBroadcaster(t *testing.T) *events.Broadcaster {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	b := events.NewBroadcaster(pool.Pool, logger)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go b.Start(ctx)

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	require.NoError(t, b.WaitReady(readyCtx), "listener must connect within 5s")

	return b
}

func TestBroadcaster_PublishReachesSubscriber(t *testing.T) {
	b := newTestBroadcaster(t)
	tenantID := uuid.New()

	ch, unsubscribe := b.Subscribe(tenantID)
	defer unsubscribe()

	b.Publish(tenantID, "alert", map[string]any{"id": "a1"})

	select {
	case ev := <-ch:
		assert.Equal(t, "alert", ev.Type)
		assert.Equal(t, map[string]any{"id": "a1"}, ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("event was not received")
	}
}

func TestBroadcaster_TenantsAreIsolated(t *testing.T) {
	b := newTestBroadcaster(t)
	tenantA, tenantB := uuid.New(), uuid.New()

	chA, unsubA := b.Subscribe(tenantA)
	defer unsubA()
	chB, unsubB := b.Subscribe(tenantB)
	defer unsubB()

	b.Publish(tenantA, "alert", nil)

	select {
	case <-chA:
	case <-time.After(2 * time.Second):
		t.Fatal("tenant A should have received its own event")
	}
	select {
	case <-chB:
		t.Fatal("tenant B must not receive tenant A's event")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestBroadcaster_PublishWithNoSubscribersDoesNotBlock(t *testing.T) {
	b := newTestBroadcaster(t)
	b.Publish(uuid.New(), "alert", nil) // must simply return, not panic or hang
}

func TestBroadcaster_UnsubscribeStopsFurtherDelivery(t *testing.T) {
	b := newTestBroadcaster(t)
	tenantID := uuid.New()

	ch, unsubscribe := b.Subscribe(tenantID)
	unsubscribe()

	b.Publish(tenantID, "alert", nil)

	_, open := <-ch
	assert.False(t, open, "the channel must be closed after unsubscribe")
}

func TestBroadcaster_SlowSubscriberDropsRatherThanBlocksPublish(t *testing.T) {
	b := newTestBroadcaster(t)
	tenantID := uuid.New()

	_, unsubscribe := b.Subscribe(tenantID) // never drained
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Publish(tenantID, "alert", i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish must not block when a subscriber's buffer is full")
	}
}

func TestBroadcaster_MultipleSubscribersOnSameTenant(t *testing.T) {
	b := newTestBroadcaster(t)
	tenantID := uuid.New()

	ch1, unsub1 := b.Subscribe(tenantID)
	defer unsub1()
	ch2, unsub2 := b.Subscribe(tenantID)
	defer unsub2()

	b.Publish(tenantID, "incident", nil)

	for _, ch := range []<-chan events.Event{ch1, ch2} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("every subscriber for the tenant should receive the event")
		}
	}
}

func TestBroadcaster_DoubleUnsubscribeIsSafe(t *testing.T) {
	b := newTestBroadcaster(t)
	_, unsubscribe := b.Subscribe(uuid.New())
	require.NotPanics(t, func() {
		unsubscribe()
		unsubscribe()
	})
}

// TestBroadcaster_CrossProcessDelivery is the regression test for the whole
// point of this rewrite: two independent Broadcaster instances (standing in
// for two different processes -- e.g. cmd/api replica A and cmd/ingest,
// which previously had no way to reach a client connected to A at all) each
// get their own pool and LISTEN connection; an event published on one must
// be delivered to a subscriber on the OTHER.
func TestBroadcaster_CrossProcessDelivery(t *testing.T) {
	publisher := newTestBroadcaster(t)
	subscriber := newTestBroadcaster(t)
	tenantID := uuid.New()

	ch, unsubscribe := subscriber.Subscribe(tenantID)
	defer unsubscribe()

	publisher.Publish(tenantID, "incident", map[string]any{"id": "cross-process"})

	select {
	case ev := <-ch:
		assert.Equal(t, "incident", ev.Type)
		assert.Equal(t, map[string]any{"id": "cross-process"}, ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("event published on one Broadcaster must reach a subscriber on another")
	}
}

// A model's reply is easily several kilobytes of markdown -- the triage report
// seen in practice was ~3 KB, and JSON escaping of its newlines and quotes
// grows it further. Postgres caps a NOTIFY payload at 8000 bytes and rejects a
// larger one outright, so an event that carried the reply was dropped without
// reaching anyone, logged only as "publish event failed".
func TestBroadcaster_OversizedPayloadStillReachesSubscriber(t *testing.T) {
	b := newTestBroadcaster(t)
	tenantID := uuid.New()
	ch, unsubscribe := b.Subscribe(tenantID)
	defer unsubscribe()

	huge := strings.Repeat("line of a long triage report with \"quotes\"\n", 400) // ~17 KB
	b.Publish(tenantID, "ai_analysis_turn", map[string]any{
		"contextId": "ctx-1",
		"runId":     7,
		"message":   map[string]any{"role": "assistant", "content": huge},
	})

	select {
	case ev := <-ch:
		assert.Equal(t, "ai_analysis_turn", ev.Type)
		payload, ok := ev.Payload.(map[string]any)
		require.True(t, ok, "payload should still be an object, got %T", ev.Payload)
		assert.Equal(t, "ctx-1", payload["contextId"], "the small identifying fields are what a client needs and must survive")
		assert.EqualValues(t, 7, payload["runId"])
		assert.NotContains(t, payload, "message", "the oversized field is what gets dropped")
		assert.Equal(t, true, payload["truncated"], "a consumer must be able to tell the payload was cut")
	case <-time.After(2 * time.Second):
		t.Fatal("an oversized event was lost: Postgres refused the NOTIFY and nothing was delivered")
	}
}

func TestBroadcaster_SmallPayloadIsUntouched(t *testing.T) {
	b := newTestBroadcaster(t)
	tenantID := uuid.New()
	ch, unsubscribe := b.Subscribe(tenantID)
	defer unsubscribe()

	b.Publish(tenantID, "alert", map[string]any{"id": "a1", "action": "analyzed"})

	select {
	case ev := <-ch:
		assert.Equal(t, map[string]any{"id": "a1", "action": "analyzed"}, ev.Payload, "no truncation marker on a payload that fit")
	case <-time.After(2 * time.Second):
		t.Fatal("event was not received")
	}
}

func TestBroadcaster_OversizedNonObjectPayloadStillDeliversTheEventType(t *testing.T) {
	b := newTestBroadcaster(t)
	tenantID := uuid.New()
	ch, unsubscribe := b.Subscribe(tenantID)
	defer unsubscribe()

	b.Publish(tenantID, "alert", strings.Repeat("x", 20000))

	select {
	case ev := <-ch:
		assert.Equal(t, "alert", ev.Type, "a client can still react to 'something changed' even with no payload")
		assert.Nil(t, ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("event was lost")
	}
}

// TestBroadcaster_TapSeesEveryTenantsEvents: a tap is process-level, so unlike a
// Subscribe (one tenant, one SSE connection) it sees events of all tenants.
func TestBroadcaster_TapSeesEveryTenantsEvents(t *testing.T) {
	b := newTestBroadcaster(t)
	type seen struct {
		tenant uuid.UUID
		typ    string
	}
	got := make(chan seen, 4)
	b.Tap(func(tenantID uuid.UUID, ev events.Event) { got <- seen{tenantID, ev.Type} })

	t1, t2 := uuid.New(), uuid.New()
	b.Publish(t1, "alert", map[string]any{"action": "received"})
	b.Publish(t2, "incident", map[string]any{"action": "created"})

	want := map[seen]bool{{t1, "alert"}: true, {t2, "incident"}: true}
	for range 2 {
		select {
		case s := <-got:
			assert.True(t, want[s], "unexpected tap event %+v", s)
			delete(want, s)
		case <-time.After(3 * time.Second):
			t.Fatalf("tap did not see every event; still waiting for %v", want)
		}
	}
}
