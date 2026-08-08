package domain

import (
	"time"

	"github.com/google/uuid"
)

type EscalationChannelType string

const (
	EscalationChannelPagerDuty EscalationChannelType = "pagerduty"
	EscalationChannelSlack     EscalationChannelType = "slack"
	EscalationChannelWebhook   EscalationChannelType = "webhook"
)

// EscalationPolicy mirrors `escalation_policies` -- Settings -> On-Call
// Escalation's per-severity rule for notifying whoever's on shift (see
// OnCallShift) when an alert of this Severity has stayed 'open' longer
// than UnacknowledgedAfterMinutes. See cmd/worker's escalation sweep and
// internal/notifier for the channels this can fire to.
type EscalationPolicy struct {
	ID                         uuid.UUID             `json:"id"`
	TenantID                   uuid.UUID             `json:"tenantId"`
	Severity                   Severity              `json:"severity"`
	UnacknowledgedAfterMinutes int                   `json:"unacknowledgedAfterMinutes"`
	ChannelType                EscalationChannelType `json:"channelType"`
	// DestinationSecretRef is never serialized -- resolved server-side only,
	// same discipline as every other secret ref (e.g. StorageConfig's).
	DestinationSecretRef string `json:"-"`
	// WebhookPayloadTemplate is only meaningful when ChannelType is
	// EscalationChannelWebhook -- nil means send notifier.WebhookSender's
	// default fixed JSON shape, same as before this field existed. When
	// set, {{title}}/{{description}}/{{severity}}/{{alertId}}/{{url}}
	// placeholders are substituted in before sending (see
	// notifier.WebhookSender.Send).
	WebhookPayloadTemplate *string   `json:"webhookPayloadTemplate,omitempty"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}
