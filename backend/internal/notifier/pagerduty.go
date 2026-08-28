package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// pagerDutyEventsURL is a var, not a const, so tests can point it at a
// local httptest.Server -- same pattern as llmclient's anthropicAPIURL.
var pagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

// PagerDutySender fires PagerDuty's Events API v2 ("Alert Events"
// integration) -- destination is the integration's routing key (a.k.a.
// "Integration Key"), copied from a PagerDuty service's Events API v2
// integration settings.
type PagerDutySender struct{}

type pagerDutyEvent struct {
	RoutingKey  string           `json:"routing_key"`
	EventAction string           `json:"event_action"`
	DedupKey    string           `json:"dedup_key,omitempty"`
	Payload     pagerDutyPayload `json:"payload"`
	Links       []pagerDutyLink  `json:"links,omitempty"`
}

type pagerDutyPayload struct {
	Summary       string            `json:"summary"`
	Source        string            `json:"source"`
	Severity      string            `json:"severity"`
	CustomDetails map[string]string `json:"custom_details,omitempty"`
}

type pagerDutyLink struct {
	Href string `json:"href"`
	Text string `json:"text"`
}

// pagerDutySeverity maps KuruOps' 5-tier severity to PagerDuty Events v2's
// 4-value enum ("critical", "error", "warning", "info") -- PagerDuty has no
// direct equivalent of KuruOps' "high"/"low" split, so "high" maps to its
// "error" (the next tier down from critical) and "low" to "warning" (still
// actionable, just not urgent) rather than either collapsing into
// "critical" or being silently dropped to "info".
func pagerDutySeverity(severity string) string {
	switch severity {
	case "critical":
		return "critical"
	case "high":
		return "error"
	case "medium", "low":
		return "warning"
	default:
		return "info"
	}
}

func (PagerDutySender) Send(ctx context.Context, destination string, n Notification) error {
	event := pagerDutyEvent{
		RoutingKey:  destination,
		EventAction: "trigger",
		DedupKey:    "kuruops-alert-" + n.AlertID,
		Payload: pagerDutyPayload{
			Summary:       n.Title,
			Source:        "KuruOps",
			Severity:      pagerDutySeverity(n.Severity),
			CustomDetails: map[string]string{"description": n.Description, "alertId": n.AlertID},
		},
	}
	if n.URL != "" {
		event.Links = []pagerDutyLink{{Href: n.URL, Text: "View in KuruOps"}}
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode pagerduty event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pagerDutyEventsURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build pagerduty request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("pagerduty request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pagerduty returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
