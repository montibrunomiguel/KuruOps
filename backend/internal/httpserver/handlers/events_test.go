package handlers_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/events"
	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/testutil"
)

// newTestBroadcaster mirrors internal/events' own test helper of the same
// name (unexported there, so duplicated here rather than exported just for
// this one caller): Publish now sends a Postgres NOTIFY, and delivery only
// happens once a LISTEN connection picks it back up, so tests need a real
// database and must wait for that connection before publishing.
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

func TestEventsHandlers_Stream_DeliversPublishedEvent(t *testing.T) {
	broadcaster := newTestBroadcaster(t)
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
	time.Sleep(300 * time.Millisecond) // let the NOTIFY round-trip back and the event be written+flushed

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
	broadcaster := newTestBroadcaster(t)
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
	time.Sleep(300 * time.Millisecond)

	cancel()
	<-done

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "belongs-to-tenant-b")
}

func TestEventsHandlers_Stream_MissingTenantContext(t *testing.T) {
	// No Publish call on this path (Stream returns 401 before ever touching
	// the broadcaster), so a nil broadcaster is safe here and avoids
	// requiring a database connection just to test an auth short-circuit.
	h := handlers.NewEventsHandlers(nil)

	req := httptest.NewRequest("GET", "/events/stream", nil)
	rec := httptest.NewRecorder()
	h.Stream(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
