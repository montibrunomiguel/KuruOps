package domain

import (
	"time"

	"github.com/google/uuid"
)

// PlaybookStep is one action line within a phase's ordered list. WebhookURL
// non-empty means this step can fire a real outbound POST -- exposed by the
// UI only for PhaseContainment steps, but not enforced structurally here,
// same "the UI narrows what the schema technically allows" choice as most
// other optional fields in this codebase.
type PlaybookStep struct {
	ID                     uuid.UUID `json:"id"`
	Text                   string    `json:"text"`
	WebhookURL             string    `json:"webhookUrl,omitempty"`
	WebhookPayloadTemplate string    `json:"webhookPayloadTemplate,omitempty"`
}

// Playbook mirrors `playbooks` + `playbook_phase_steps`. Steps are grouped
// by NIST phase, one ordered slice of steps per phase — matches the
// "one textarea per NIST phase, one action per line" create/edit form in
// the design handoff.
type Playbook struct {
	ID       uuid.UUID `json:"id"`
	TenantID uuid.UUID `json:"tenantId"`
	Title    string    `json:"title"`
	Category string    `json:"category"`
	// AlertNamePattern is a SQL LIKE-style pattern (case-insensitively
	// matched via ILIKE against alerts.title, `%` as wildcard) used to
	// auto-assign this playbook to a new alert at ingest time -- see
	// AlertService.Ingest and PlaybookRepository.MatchForAlertTitle. Empty
	// means this playbook never matches by name, only by IsDefault.
	AlertNamePattern string `json:"alertNamePattern"`
	// IsDefault marks the one playbook per tenant assigned to an alert when
	// no AlertNamePattern matches -- enforced by a partial unique index
	// (playbooks_one_default_per_tenant), and by PlaybookRepository always
	// unsetting any other tenant playbook's flag when this one is set.
	IsDefault   bool                             `json:"isDefault"`
	Description string                           `json:"description"`
	Keywords    []string                         `json:"keywords"`
	Steps       map[IncidentPhase][]PlaybookStep `json:"steps"`
	CreatedBy   *uuid.UUID                       `json:"createdBy,omitempty"`
	CreatedAt   time.Time                        `json:"createdAt"`
	UpdatedAt   time.Time                        `json:"updatedAt"`
}

type SavePlaybookStepInput struct {
	Text                   string
	WebhookURL             string
	WebhookPayloadTemplate string
}

type SavePlaybookInput struct {
	Title            string
	Category         string
	Description      string
	Keywords         []string
	AlertNamePattern string
	IsDefault        bool
	Steps            map[IncidentPhase][]SavePlaybookStepInput
}
