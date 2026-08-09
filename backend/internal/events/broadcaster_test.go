package events_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/events"
	"github.com/argusops/argusops/internal/testutil"
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
