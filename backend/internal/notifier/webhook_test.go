package notifier_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/notifier"
)

func TestWebhookSender_Send(t *testing.T) {
	// WebhookSender dials through httpguard (see webhook.go), which refuses
	// loopback destinations by default -- httptest.NewServer always binds
	// to 127.0.0.1, so this test needs the same escape hatch a genuine
	// on-prem deployment would set.
	t.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "true")

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := notifier.WebhookSender{}.Send(t.Context(), srv.URL, notifier.Notification{
		Title: "Malware detected", Description: "d", Severity: "critical", AlertID: "a1", URL: "https://argusops.example/alerts/a1",
	})
	require.NoError(t, err)

	assert.Equal(t, "Malware detected", gotBody["title"])
	assert.Equal(t, "critical", gotBody["severity"])
	assert.Equal(t, "a1", gotBody["alertId"])
	assert.Equal(t, "https://argusops.example/alerts/a1", gotBody["url"])
}

func TestWebhookSender_Send_ErrorResponse(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "true")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := notifier.WebhookSender{}.Send(t.Context(), srv.URL, notifier.Notification{Title: "t", Severity: "low", AlertID: "a1"})
	assert.ErrorContains(t, err, "500")
}
