package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTracingMiddleware_RecordsSpanWithHTTPAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := provider.Tracer("test")

	handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, "POST /api/v1/alerts", spans[0].Name())

	attrs := map[string]string{}
	for _, kv := range spans[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	require.Equal(t, "POST", attrs["http.method"])
	require.Equal(t, "/api/v1/alerts", attrs["http.route"])
	require.Equal(t, "201", attrs["http.status_code"])
	require.NotEqual(t, codes.Error, spans[0].Status().Code)
}

func TestTracingMiddleware_MarksSpanErrorOn5xx(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := provider.Tracer("test")

	handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status().Code)
}
