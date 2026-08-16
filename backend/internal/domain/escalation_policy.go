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

// EscalationPolicy mirrors `escalation_policies` -- Settings -> Escala de
// Acionamento's per-severity escalation chain: an ordered sequence of Steps,
// each pointing at an OnCallSchedule to notify. Two independent triggers
// drive it (see cmd/worker's sweepEscalations and AlertHandlers.escalate):
// the automatic SLA loop (an alert that's stayed unacknowledged past a
// step's DelayMinutes -- keeps cycling through every step, wrapping back to
// the first, until the alert is attended to) and a human manually
// escalating an alert (advances exactly one step, independent counter,
// never loops). See alerts.sla_escalation_step / alerts.manual_escalation_step.
type EscalationPolicy struct {
	ID        uuid.UUID        `json:"id"`
	TenantID  uuid.UUID        `json:"tenantId"`
	Severity  Severity         `json:"severity"`
	Steps     []EscalationStep `json:"steps"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// EscalationStep is one link in an EscalationPolicy's chain -- Position is
// 0-indexed and dense (0..len(Steps)-1), matching the array index the
// worker's wraparound math (position % len(steps)) relies on.
type EscalationStep struct {
	ID       uuid.UUID `json:"id"`
	PolicyID uuid.UUID `json:"policyId"`
	Position int       `json:"position"`
	// ScheduleID is which OnCallSchedule this step resolves the on-call
	// analyst against -- not necessarily the tenant's default schedule (a
	// step can point at any schedule, e.g. "escalate to the DBA team's
	// rotation"). ScheduleName is denormalized via a join, read-only.
	ScheduleID   uuid.UUID `json:"scheduleId"`
	ScheduleName string    `json:"scheduleName"`
	// DelayMinutes is how long to wait after the previous event (the alert
	// opening, for step 0; the previous step's fire time, for later steps)
	// before this step fires -- "a cada X tempo uma nova escala é acionada."
	DelayMinutes int                   `json:"delayMinutes"`
	ChannelType  EscalationChannelType `json:"channelType"`
	// DestinationSecretRef is never serialized -- resolved server-side only,
	// same discipline as every other stored secret ref in this app.
	DestinationSecretRef string `json:"-"`
	// WebhookPayloadTemplate is only meaningful when ChannelType is
	// EscalationChannelWebhook -- nil means send notifier.WebhookSender's
	// default fixed JSON shape. When set, the placeholders in
	// notifier.WebhookPlaceholders (including the resolved analyst's
	// name/email/phone) are substituted in before sending.
	WebhookPayloadTemplate *string `json:"webhookPayloadTemplate,omitempty"`
}

// SaveEscalationPolicyInput/SaveEscalationStepInput are
// EscalationPolicyService.Save's input shape -- the whole chain is
// resubmitted and wholesale-replaced on every save, same idiom
// PlaybookService.Save uses for a playbook's steps.
type SaveEscalationPolicyInput struct {
	Severity Severity
	Steps    []SaveEscalationStepInput
}

// SaveEscalationStepInput.Destination is plaintext -- resolved into
// DestinationSecretRef by the service, same convention as every other
// stored credential in this app; "" means keep the existing secret for that
// position (see EscalationPolicyService.Save).
type SaveEscalationStepInput struct {
	ScheduleID             uuid.UUID
	DelayMinutes           int
	ChannelType            EscalationChannelType
	Destination            string
	WebhookPayloadTemplate string
}
