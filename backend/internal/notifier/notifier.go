// Package notifier sends an outbound notification when an on-call
// escalation policy fires (see cmd/worker's escalation sweep and
// service.EscalationPolicyService). This app's existing "webhooks"
// (internal/service/webhook_service.go) are inbound-only (alert ingest) --
// this is genuinely new outbound HTTP infrastructure, with its own
// timeout/retry-free (best-effort, single attempt) semantics: a failed
// escalation is logged by the worker, not retried, since the next sweep
// tick will simply try again on the next unacknowledged alert it finds
// (this one included, until it's acknowledged or a human notices the log).
// Nothing here has been exercised against a real PagerDuty/Slack
// account -- treat it as a solid starting point to validate before relying
// on it for anything on-call-critical.
package notifier

import (
	"context"
	"fmt"
)

// Notification is a channel-agnostic escalation payload -- each Sender
// translates it into its own wire format.
type Notification struct {
	Title       string
	Description string
	// Severity is ArgusOps' own domain.Severity string ("critical", "high",
	// "medium", "low", "informational") -- each Sender maps it to whatever
	// scale its own API expects.
	Severity string
	AlertID  string
	// URL is the alert's detail page, so an on-call responder can click
	// through -- optional; some channels render it as a link, others just
	// pass it through as a field.
	URL string
}

// Sender delivers one Notification to destination -- a PagerDuty routing
// key, a Slack incoming-webhook URL, or an arbitrary URL for the generic
// webhook channel, depending on which Sender it is.
type Sender interface {
	Send(ctx context.Context, destination string, n Notification) error
}

// New returns the Sender for channelType, matching
// domain.EscalationChannelType's values.
func New(channelType string) (Sender, error) {
	switch channelType {
	case "pagerduty":
		return PagerDutySender{}, nil
	case "slack":
		return SlackSender{}, nil
	case "webhook":
		return WebhookSender{}, nil
	default:
		return nil, fmt.Errorf("unknown escalation channel type %q", channelType)
	}
}
