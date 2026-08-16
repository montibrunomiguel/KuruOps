// Package notifier sends an outbound notification when an on-call
// escalation policy fires (see cmd/worker's escalation sweep and
// service.EscalationPolicyService). This app's existing "webhooks"
// (internal/service/webhook_service.go) are inbound-only (alert ingest) --
// this is genuinely new outbound HTTP infrastructure. Every Sender returned
// by New/NewForPolicy is wrapped in RetryingSender (see retry.go), so a
// single transient failure (a dropped connection, a momentary 5xx) doesn't
// silently drop the page -- a failure that survives all retry attempts is
// still just logged by the worker, not retried across sweep ticks, since
// the next tick will simply try again on the next unacknowledged alert it
// finds (this one included, until it's acknowledged or a human notices the
// log). Validated end-to-end against real PagerDuty/Slack/webhook
// destinations as part of the production-readiness pass that added the
// retry wrapper -- see docs/OPERATIONS.md.
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
	// AnalystName/AnalystEmail/AnalystPhone are the on-call analyst resolved
	// against the escalation step's OnCallSchedule at the moment it fired --
	// empty when no one is currently on shift for that schedule. Only
	// WebhookSender's custom-template path substitutes these in (see
	// WebhookPlaceholders); the fixed webhook JSON shape and the
	// PagerDuty/Slack senders don't reference them.
	AnalystName  string
	AnalystEmail string
	AnalystPhone string
}

// Sender delivers one Notification to destination -- a PagerDuty routing
// key, a Slack incoming-webhook URL, or an arbitrary URL for the generic
// webhook channel, depending on which Sender it is.
type Sender interface {
	Send(ctx context.Context, destination string, n Notification) error
}

// New returns the Sender for channelType, matching
// domain.EscalationChannelType's values -- wrapped in RetryingSender so
// every channel gets the same bounded retry-with-backoff behavior.
func New(channelType string) (Sender, error) {
	switch channelType {
	case "pagerduty":
		return RetryingSender{Inner: PagerDutySender{}}, nil
	case "slack":
		return RetryingSender{Inner: SlackSender{}}, nil
	case "webhook":
		return RetryingSender{Inner: WebhookSender{}}, nil
	default:
		return nil, fmt.Errorf("unknown escalation channel type %q", channelType)
	}
}

// NewForPolicy is New, but for the webhook channel also attaches a
// per-tenant payload template (EscalationPolicy.WebhookPayloadTemplate) --
// nil or empty falls back to WebhookSender's default fixed payload shape.
// webhookTemplate is ignored for every other channel type.
func NewForPolicy(channelType string, webhookTemplate *string) (Sender, error) {
	if channelType != "webhook" {
		return New(channelType)
	}
	template := ""
	if webhookTemplate != nil {
		template = *webhookTemplate
	}
	return RetryingSender{Inner: WebhookSender{Template: template}}, nil
}
