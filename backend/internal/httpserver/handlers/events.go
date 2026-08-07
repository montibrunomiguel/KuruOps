package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/argusops/argusops/internal/events"
	"github.com/argusops/argusops/internal/httpserver/middleware"
)

// sseKeepAliveInterval bounds how long the connection can go silent before
// a ping comment is sent -- keeps intermediate proxies/load balancers (and
// some browsers) from treating a quiet-but-healthy stream as dead and
// closing it.
const sseKeepAliveInterval = 25 * time.Second

type EventsHandlers struct {
	broadcaster *events.Broadcaster
}

func NewEventsHandlers(b *events.Broadcaster) *EventsHandlers {
	return &EventsHandlers{broadcaster: b}
}

// Stream serves GET /events/stream: a long-lived text/event-stream
// connection pushing this tenant's alert/incident events as they happen
// (see events.Broadcaster), so Dashboard/Alerts/Incidents pages can update
// live instead of only on a manual reload. Not resourceAccess-gated --
// same reasoning as /dashboard: a viewer scoped to just one resource type
// still gets a coherent stream, they just won't act on event types their
// own pages don't render.
func (h *EventsHandlers) Stream(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ch, unsubscribe := h.broadcaster.Subscribe(tenantID)
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Nginx buffers proxied responses by default, which would defeat SSE
	// entirely (events wouldn't reach the browser until the buffer filled
	// or the connection closed) -- see frontend/nginx.conf's /api/v1 proxy
	// block, which must set proxy_buffering off for this path.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(sseKeepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(ev.Payload)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
