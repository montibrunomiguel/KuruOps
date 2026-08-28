package notifier_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/notifier"
)

func TestSlackSender_Send(t *testing.T) {
	// SlackSender now dials through httpguard too (see slack.go), which
	// refuses loopback destinations by default -- httptest.NewServer always
	// binds to 127.0.0.1, so this test needs the same escape hatch a
	// genuine on-prem deployment would set.
	t.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "true")

	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	err := notifier.SlackSender{}.Send(t.Context(), srv.URL, notifier.Notification{
		Title: "Suspicious login", Description: "5 failed attempts from 10.0.0.5",
		Severity: "high", AlertID: "a1", URL: "https://kuruops.example/alerts/a1",
	})
	require.NoError(t, err)

	assert.Contains(t, gotBody["text"], "Suspicious login")
	assert.Contains(t, gotBody["text"], "5 failed attempts from 10.0.0.5")
	assert.Contains(t, gotBody["text"], "https://kuruops.example/alerts/a1")
}

func TestSlackSender_Send_ErrorResponse(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "true")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no_service"))
	}))
	defer srv.Close()

	err := notifier.SlackSender{}.Send(t.Context(), srv.URL, notifier.Notification{Title: "t", Severity: "low", AlertID: "a1"})
	assert.ErrorContains(t, err, "404")
}
