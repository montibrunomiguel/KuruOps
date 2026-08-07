package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/argusops/argusops/internal/events"
	"github.com/argusops/argusops/internal/httpserver/handlers"
)

func TestEventsHandlers_Stream_DeliversPublishedEvent(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	h := handlers.NewEventsHandlers(broadcaster)
	tenantID := uuid.New()

	req := withClaims(httptest.NewRequest("GET", "/events/stream", nil), tenantID, uuid.New(), nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.Stream(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond) // let Stream's Subscribe register
	broadcaster.Publish(tenantID, "alert", map[string]string{"id": "a1"})
	time.Sleep(50 * time.Millisecond) // let the event be written+flushed

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stream did not exit after context cancellation")
	}
	// Only read rec.Body after done is closed -- Stream's goroutine has
	// stopped writing to it by then, so this isn't a data race.

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	assert.Contains(t, body, "event: alert")
	assert.Contains(t, body, `"id":"a1"`)
}

func TestEventsHandlers_Stream_TenantsAreIsolated(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	h := handlers.NewEventsHandlers(broadcaster)
	tenantA, tenantB := uuid.New(), uuid.New()

	req := withClaims(httptest.NewRequest("GET", "/events/stream", nil), tenantA, uuid.New(), nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.Stream(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	broadcaster.Publish(tenantB, "alert", map[string]string{"id": "belongs-to-tenant-b"})
	time.Sleep(50 * time.Millisecond)

	cancel()
	<-done

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "belongs-to-tenant-b")
}

func TestEventsHandlers_Stream_MissingTenantContext(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	h := handlers.NewEventsHandlers(broadcaster)

	req := httptest.NewRequest("GET", "/events/stream", nil)
	rec := httptest.NewRecorder()
	h.Stream(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
