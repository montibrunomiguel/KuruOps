package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WebhookSender POSTs the notification as JSON to an arbitrary destination
// URL -- the escape hatch for any receiver that isn't PagerDuty or Slack (a
// custom on-call tool, an internal relay, etc). Template, if non-empty, is
// the tenant's own payload shape (EscalationPolicy.WebhookPayloadTemplate)
// with {{title}}/{{description}}/{{severity}}/{{alertId}}/{{url}}
// placeholders substituted in -- an empty Template sends the original fixed
// JSON shape unchanged.
type WebhookSender struct {
	Template string
}

type webhookPayload struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	AlertID     string `json:"alertId"`
	URL         string `json:"url,omitempty"`
}

// WebhookPlaceholders are the substitution tokens available in a custom
// webhook payload template -- exported so the Settings UI can show the
// exact same list, and so EscalationPolicyService can validate a template
// by substituting sample values before ever saving it.
var WebhookPlaceholders = []string{"{{title}}", "{{description}}", "{{severity}}", "{{alertId}}", "{{url}}"}

// RenderWebhookTemplate substitutes n's fields into template's placeholders.
// Plain string replacement, not a templating engine -- the payload shape a
// tenant wants is arbitrary JSON they write themselves, this only fills in
// the blanks.
func RenderWebhookTemplate(template string, n Notification) string {
	replacer := strings.NewReplacer(
		"{{title}}", n.Title,
		"{{description}}", n.Description,
		"{{severity}}", n.Severity,
		"{{alertId}}", n.AlertID,
		"{{url}}", n.URL,
	)
	return replacer.Replace(template)
}

func (s WebhookSender) Send(ctx context.Context, destination string, n Notification) error {
	var body []byte
	if s.Template != "" {
		body = []byte(RenderWebhookTemplate(s.Template, n))
	} else {
		b, err := json.Marshal(webhookPayload(n))
		if err != nil {
			return fmt.Errorf("encode webhook payload: %w", err)
		}
		body = b
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
