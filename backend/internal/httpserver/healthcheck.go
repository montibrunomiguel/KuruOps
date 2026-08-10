package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/argusops/argusops/internal/httpserver/middleware"
)

// HealthCheck returns a handler for GET /healthz that pings pool with a
// short timeout and reports 503 if the database isn't reachable, instead of
// unconditionally returning 200 regardless of whether the app can actually
// serve a real request. Shared by cmd/api and cmd/ingest.
func HealthCheck(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unreachable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}

// Livez returns a handler for GET /livez that unconditionally reports 200 --
// a pure "this process is alive" check with no downstream dependency,
// unlike HealthCheck. Meant for the liveness probe specifically:
// HealthCheck's DB check is correct for readiness (pull the pod from the
// Service until it can serve a real request) but wrong for liveness -- a
// transient Postgres blip would otherwise make kubelet kill and restart
// every api/ingest/worker replica at once, turning a brief DB hiccup into a
// synchronized restart storm instead of just routing around it.
func Livez(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// WrapWithObservability applies the same request-id / correlated-logging /
// metrics middleware chain NewRouter wires into the chi router (see
// router.go), for a binary whose own routing isn't chi-based (cmd/worker,
// cmd/ingest's plain http.ServeMux) -- keeps that chain defined and tested
// in one place instead of hand-assembled identically in every cmd/main.go.
func WrapWithObservability(base http.Handler, logger *slog.Logger) http.Handler {
	h := MetricsMiddleware(base)
	h = middleware.RequestLogger(logger)(h)
	h = chimw.RequestID(h)
	return h
}
