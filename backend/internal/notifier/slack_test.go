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

func TestSlackSender_Send(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	err := notifier.SlackSender{}.Send(t.Context(), srv.URL, notifier.Notification{
		Title: "Suspicious login", Description: "5 failed attempts from 10.0.0.5",
		Severity: "high", AlertID: "a1", URL: "https://argusops.example/alerts/a1",
	})
	require.NoError(t, err)

	assert.Contains(t, gotBody["text"], "Suspicious login")
	assert.Contains(t, gotBody["text"], "5 failed attempts from 10.0.0.5")
	assert.Contains(t, gotBody["text"], "https://argusops.example/alerts/a1")
}

func TestSlackSender_Send_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no_service"))
	}))
	defer srv.Close()

	err := notifier.SlackSender{}.Send(t.Context(), srv.URL, notifier.Notification{Title: "t", Severity: "low", AlertID: "a1"})
	assert.ErrorContains(t, err, "404")
}
