package middleware

import (
	"context"
	"log/slog"
	"net/http"

	chimw "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"
)

type loggerCtxKey struct{}

// RequestLogger stores a request-scoped slog.Logger (base with a
// "request_id" attribute copied from chi's own RequestID -- see
// chimw.RequestID, which must run before this -- plus a "trace_id"
// attribute when TracingMiddleware has already started a span on this
// request's context, i.e. when it runs before this middleware too) in the
// request context. LoggerFromContext retrieves it. Without this, chi's own
// access log line (chimw.Logger) and an application log line about the same
// request (e.g. "ingest alert failed") are two uncorrelated streams with no
// shared id to grep by -- and without trace_id specifically, a log line and
// the distributed trace it happened during have no shared id either.
func RequestLogger(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger := base
			if reqID := chimw.GetReqID(r.Context()); reqID != "" {
				logger = logger.With("request_id", reqID)
			}
			if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
				logger = logger.With("trace_id", sc.TraceID().String())
			}
			ctx := context.WithValue(r.Context(), loggerCtxKey{}, logger)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// LoggerFromContext returns the request-scoped logger RequestLogger stored
// in ctx, or fallback if none is present -- a call site not reached via a
// RequestLogger-wrapped handler (a background goroutine, a test that didn't
// wire the middleware) still gets a usable logger, just without the
// request_id attribute.
func LoggerFromContext(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(loggerCtxKey{}).(*slog.Logger); ok {
		return l
	}
	return fallback
}
