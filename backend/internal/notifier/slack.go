package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SlackSender posts to a Slack "Incoming Webhook" -- destination is the
// full webhook URL (https://hooks.slack.com/services/...) an admin creates
// in Slack's own app settings and pastes into Settings -> On-Call
// Escalation. Nothing here validates that the pasted URL is actually a
// hooks.slack.com address, so this is exactly as free-text as
// WebhookSender's destination -- it goes through the same SSRF-guarded
// client (guardedHTTPClient, defined in webhook.go) rather than the plain
// shared httpClient PagerDutySender uses for its fixed vendor host.
type SlackSender struct{}

type slackMessage struct {
	Text string `json:"text"`
}

var severityEmoji = map[string]string{
	"critical": "🔴", "high": "🟠", "medium": "🟡", "low": "🔵", "informational": "⚪",
}

func (SlackSender) Send(ctx context.Context, destination string, n Notification) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s *%s* (%s)\n", severityEmoji[n.Severity], n.Title, n.Severity)
	if n.Description != "" {
		fmt.Fprintf(&b, "%s\n", n.Description)
	}
	if n.URL != "" {
		fmt.Fprintf(&b, "<%s|View in ArgusOps>", n.URL)
	}

	body, err := json.Marshal(slackMessage{Text: b.String()})
	if err != nil {
		return fmt.Errorf("encode slack message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := guardedHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
