package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"
)

func TestRequestLogger_AttachesRequestIDFromChi(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := chimw.RequestID(RequestLogger(base)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		LoggerFromContext(r.Context(), base).Info("test event")
	})))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v, raw: %s", err, buf.String())
	}
	if _, ok := entry["request_id"]; !ok {
		t.Fatalf("expected request_id attribute in log entry, got %v", entry)
	}
}

func TestLoggerFromContext_FallsBackWhenNoRequestLoggerRan(t *testing.T) {
	fallback := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	got := LoggerFromContext(t.Context(), fallback)
	if got != fallback {
		t.Fatal("expected fallback logger when context has no request-scoped logger")
	}
}
