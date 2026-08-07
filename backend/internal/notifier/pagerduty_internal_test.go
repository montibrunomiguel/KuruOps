package notifier

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPagerDutySender_Send lives in-package (not notifier_test) so it can
// redirect pagerDutyEventsURL at a local httptest.Server -- the sender
// always POSTs to the real PagerDuty endpoint otherwise.
func TestPagerDutySender_Send(t *testing.T) {
	var gotBody pagerDutyEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	original := pagerDutyEventsURL
	pagerDutyEventsURL = srv.URL
	t.Cleanup(func() { pagerDutyEventsURL = original })

	err := PagerDutySender{}.Send(t.Context(), "R0UT1NG-KEY", Notification{
		Title: "Ransomware behavior detected", Description: "host-01 exhibiting mass file encryption",
		Severity: "critical", AlertID: "a1", URL: "https://argusops.example/alerts/a1",
	})
	require.NoError(t, err)

	assert.Equal(t, "R0UT1NG-KEY", gotBody.RoutingKey)
	assert.Equal(t, "trigger", gotBody.EventAction)
	assert.Equal(t, "argusops-alert-a1", gotBody.DedupKey)
	assert.Equal(t, "Ransomware behavior detected", gotBody.Payload.Summary)
	assert.Equal(t, "critical", gotBody.Payload.Severity)
	require.Len(t, gotBody.Links, 1)
	assert.Equal(t, "https://argusops.example/alerts/a1", gotBody.Links[0].Href)
}

func TestPagerDutySender_Send_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"invalid event"}`))
	}))
	defer srv.Close()

	original := pagerDutyEventsURL
	pagerDutyEventsURL = srv.URL
	t.Cleanup(func() { pagerDutyEventsURL = original })

	err := PagerDutySender{}.Send(t.Context(), "bad-key", Notification{Title: "t", Severity: "high", AlertID: "a1"})
	assert.ErrorContains(t, err, "400")
}

func TestPagerDutySeverity(t *testing.T) {
	cases := map[string]string{
		"critical": "critical", "high": "error", "medium": "warning",
		"low": "warning", "informational": "info", "unknown": "info",
	}
	for in, want := range cases {
		assert.Equal(t, want, pagerDutySeverity(in), "severity %q", in)
	}
}
