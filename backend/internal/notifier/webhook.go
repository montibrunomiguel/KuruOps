package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebhookSender POSTs the notification as plain JSON to an arbitrary
// destination URL -- the escape hatch for any receiver that isn't
// PagerDuty or Slack (a custom on-call tool, an internal relay, etc).
type WebhookSender struct{}

type webhookPayload struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	AlertID     string `json:"alertId"`
	URL         string `json:"url,omitempty"`
}

func (WebhookSender) Send(ctx context.Context, destination string, n Notification) error {
	body, err := json.Marshal(webhookPayload(n))
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
