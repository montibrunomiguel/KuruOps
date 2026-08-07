package events_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/events"
)

func TestBroadcaster_PublishReachesSubscriber(t *testing.T) {
	b := events.NewBroadcaster()
	tenantID := uuid.New()

	ch, unsubscribe := b.Subscribe(tenantID)
	defer unsubscribe()

	b.Publish(tenantID, "alert", map[string]string{"id": "a1"})

	select {
	case ev := <-ch:
		assert.Equal(t, "alert", ev.Type)
		assert.Equal(t, map[string]string{"id": "a1"}, ev.Payload)
	case <-time.After(time.Second):
		t.Fatal("event was not received")
	}
}

func TestBroadcaster_TenantsAreIsolated(t *testing.T) {
	b := events.NewBroadcaster()
	tenantA, tenantB := uuid.New(), uuid.New()

	chA, unsubA := b.Subscribe(tenantA)
	defer unsubA()
	chB, unsubB := b.Subscribe(tenantB)
	defer unsubB()

	b.Publish(tenantA, "alert", nil)

	select {
	case <-chA:
	case <-time.After(time.Second):
		t.Fatal("tenant A should have received its own event")
	}
	select {
	case <-chB:
		t.Fatal("tenant B must not receive tenant A's event")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBroadcaster_PublishWithNoSubscribersDoesNotBlock(t *testing.T) {
	b := events.NewBroadcaster()
	b.Publish(uuid.New(), "alert", nil) // must simply return, not panic or hang
}

func TestBroadcaster_UnsubscribeStopsFurtherDelivery(t *testing.T) {
	b := events.NewBroadcaster()
	tenantID := uuid.New()

	ch, unsubscribe := b.Subscribe(tenantID)
	unsubscribe()

	b.Publish(tenantID, "alert", nil)

	_, open := <-ch
	assert.False(t, open, "the channel must be closed after unsubscribe")
}

func TestBroadcaster_SlowSubscriberDropsRatherThanBlocksPublish(t *testing.T) {
	b := events.NewBroadcaster()
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
	case <-time.After(time.Second):
		t.Fatal("Publish must not block when a subscriber's buffer is full")
	}
}

func TestBroadcaster_MultipleSubscribersOnSameTenant(t *testing.T) {
	b := events.NewBroadcaster()
	tenantID := uuid.New()

	ch1, unsub1 := b.Subscribe(tenantID)
	defer unsub1()
	ch2, unsub2 := b.Subscribe(tenantID)
	defer unsub2()

	b.Publish(tenantID, "incident", nil)

	for _, ch := range []<-chan events.Event{ch1, ch2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("every subscriber for the tenant should receive the event")
		}
	}
}

func TestBroadcaster_DoubleUnsubscribeIsSafe(t *testing.T) {
	b := events.NewBroadcaster()
	_, unsubscribe := b.Subscribe(uuid.New())
	require.NotPanics(t, func() {
		unsubscribe()
		unsubscribe()
	})
}
