package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracingMiddleware starts one span per request, named by method + route
// pattern (e.g. "GET /api/v1/alerts/{id}", not the raw path -- keeps span
// names low-cardinality regardless of how many distinct alert IDs are hit).
// Must run after chimw.RequestID (so RequestLogger can pull the resulting
// trace ID alongside request_id -- see LoggerFromContext) and, in NewRouter,
// before RequestLogger itself.
func TracingMiddleware(tracer trace.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), r.Method+" "+r.URL.Path)
			defer span.End()

			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r.WithContext(ctx))

			route := chi.RouteContext(ctx)
			routePattern := r.URL.Path
			if route != nil && route.RoutePattern() != "" {
				routePattern = route.RoutePattern()
			}
			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", routePattern),
				attribute.Int("http.status_code", ww.Status()),
			)
			if ww.Status() >= 500 {
				span.SetStatus(codes.Error, http.StatusText(ww.Status()))
			}
		})
	}
}
