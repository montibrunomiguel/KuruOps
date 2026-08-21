package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type IncidentPhase string

const (
	PhaseNew               IncidentPhase = "new"
	PhaseDetectionAnalysis IncidentPhase = "detection_analysis"
	PhaseContainment       IncidentPhase = "containment"
	PhaseEradication       IncidentPhase = "eradication"
	PhaseRecovery          IncidentPhase = "recovery"
	PhasePostIncident      IncidentPhase = "post_incident"
)

// NISTPhaseOrder is the canonical NIST IR lifecycle order. The prototype
// allows jumping directly to any phase by design (no forced linear order),
// but IncidentService uses this to detect a forward skip and log it —
// "real implementation may want to enforce or warn on skipping" per the
// design handoff.
var NISTPhaseOrder = []IncidentPhase{
	PhaseNew, PhaseDetectionAnalysis, PhaseContainment,
	PhaseEradication, PhaseRecovery, PhasePostIncident,
}

// Index returns the phase's position in NISTPhaseOrder, or -1 if unknown.
func (p IncidentPhase) Index() int {
	for i, phase := range NISTPhaseOrder {
		if phase == p {
			return i
		}
	}
	return -1
}

type IncidentPriority string

const (
	PriorityP1 IncidentPriority = "p1"
	PriorityP2 IncidentPriority = "p2"
	PriorityP3 IncidentPriority = "p3"
	PriorityP4 IncidentPriority = "p4"
)

// DefaultPriorityForSeverity is the starting point on the NIST
// severity x priority matrix (see NistMatrixPanel.tsx on the frontend,
// where an analyst can always override the cell after creation) --
// Critical/High map to the two urgent priorities and Medium/Low/
// Informational fall back to routine ones, per NIST 800-61's
// impact-based prioritization guidance. Used when a new incident is
// created without an explicit priority (e.g. escalating an alert).
func DefaultPriorityForSeverity(s Severity) IncidentPriority {
	switch s {
	case SeverityCritical:
		return PriorityP1
	case SeverityHigh:
		return PriorityP2
	case SeverityMedium:
		return PriorityP3
	default: // SeverityLow, SeverityInformational, and any unknown value
		return PriorityP4
	}
}

// Incident mirrors the `incidents` table. Phase transitions, severity/priority
// changes, and description edits all go through IncidentService — never
// write this struct straight to the repository — because phase changes also
// need a corresponding incident_status_history row and event.
type Incident struct {
	ID          uuid.UUID        `json:"id"`
	TenantID    uuid.UUID        `json:"tenantId"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Severity    Severity         `json:"severity"`
	Priority    IncidentPriority `json:"priority"`
	Phase       IncidentPhase    `json:"phase"`
	// Assignees is resolved via IncidentRepository.AssigneesForIncidents at
	// read time (a join against incident_assignees + users), never
	// denormalized -- an incident can have zero or more assignees, and
	// membership changes after creation (see IncidentService.SetAssignees),
	// so a live join always reflects who's currently assigned.
	Assignees []UserSummary `json:"assignees"`
	// Roles is the NIST-800-61-style team-role assignment (Commander,
	// Incident Handler, Communications Lead, Privacy Officer, Technical
	// Lead) -- additive to Assignees above, not a replacement: Assignees is
	// "who's generally working this," Roles is "who holds which formal
	// role." Only populated by IncidentRepository.Get (the detail page),
	// not List -- the incident list view has no use for it, same reasoning
	// Assignees itself doesn't bother batch-loading there either.
	Roles       []IncidentRoleAssignment `json:"roles"`
	Tags        []string                 `json:"tags"`
	SLADueAt    *time.Time               `json:"slaDueAt,omitempty"`
	SLABreached bool                     `json:"slaBreached"`
	OpenedAt    time.Time                `json:"openedAt"`
	ClosedAt    *time.Time               `json:"closedAt,omitempty"`
	CreatedAt   time.Time                `json:"createdAt"`
	UpdatedAt   time.Time                `json:"updatedAt"`

	// LatestAnalysis/LatestAnalysisStatus/LatestAnalysisError -- see
	// domain.Alert's fields of the same name for the full doc comment; same
	// mechanism, resolved via IncidentService's own optional
	// EnableAnalysisLookup dependency.
	LatestAnalysis       *string `json:"latestAnalysis,omitempty"`
	LatestAnalysisStatus *string `json:"latestAnalysisStatus,omitempty"`
	LatestAnalysisError  *string `json:"latestAnalysisError,omitempty"`
}

// IncidentRole is one of the NIST 800-61 incident-response team roles.
// RoleCommander and RoleTechnicalLead are single-assignee (enforced by a
// partial unique index in db/migrations/0001_initial_schema.up.sql
// (incident_role_assignments_single_commander/_single_technical_lead));
// the other three allow any number of people.
type IncidentRole string

const (
	RoleCommander          IncidentRole = "commander"
	RoleIncidentHandler    IncidentRole = "incident_handler"
	RoleCommunicationsLead IncidentRole = "communications_lead"
	RolePrivacyOfficer     IncidentRole = "privacy_officer"
	RoleTechnicalLead      IncidentRole = "technical_lead"
)

// IncidentRoles lists every valid role, in the display order the frontend
// renders the "Team Roles" section -- Commander and Technical Lead first
// (the two single-assignee roles), then the multi-assignee ones.
var IncidentRoles = []IncidentRole{
	RoleCommander, RoleTechnicalLead, RoleIncidentHandler, RoleCommunicationsLead, RolePrivacyOfficer,
}

// SingleAssignee reports whether role permits at most one person at a
// time -- Commander and Technical Lead, per NIST 800-61's convention that
// both are individually-accountable roles, not a team.
func (r IncidentRole) SingleAssignee() bool {
	return r == RoleCommander || r == RoleTechnicalLead
}

func (r IncidentRole) Valid() bool {
	for _, v := range IncidentRoles {
		if v == r {
			return true
		}
	}
	return false
}

// IncidentRoleAssignment is one row of incident_role_assignments, joined
// with the assignee's name for display.
type IncidentRoleAssignment struct {
	Role IncidentRole `json:"role"`
	User UserSummary  `json:"user"`
}

type CreateIncidentInput struct {
	Title       string
	Description string
	Severity    Severity
	Priority    IncidentPriority
	AssigneeIDs []uuid.UUID
	Tags        []string
}

// IncidentStatusHistoryEntry is one row per NIST phase entered. EnteredAt is
// never mutated after insert; a correction fills CorrectedEnteredAt /
// CorrectedAt / CorrectedBy / CorrectionReason instead, so the original
// value stays intact for audit (see db/migrations/0001_initial_schema.up.sql).
type IncidentStatusHistoryEntry struct {
	ID                 uuid.UUID     `json:"id"`
	IncidentID         uuid.UUID     `json:"incidentId"`
	TenantID           uuid.UUID     `json:"tenantId"`
	Phase              IncidentPhase `json:"phase"`
	EnteredAt          time.Time     `json:"enteredAt"`
	CorrectedEnteredAt *time.Time    `json:"correctedEnteredAt,omitempty"`
	CorrectedAt        *time.Time    `json:"correctedAt,omitempty"`
	CorrectedBy        *uuid.UUID    `json:"correctedBy,omitempty"`
	CorrectionReason   *string       `json:"correctionReason,omitempty"`
	CreatedAt          time.Time     `json:"createdAt"`
}

// EffectiveEnteredAt is what the UI/KPIs should display and aggregate on —
// the correction if one exists, otherwise the original.
func (e IncidentStatusHistoryEntry) EffectiveEnteredAt() time.Time {
	if e.CorrectedEnteredAt != nil {
		return *e.CorrectedEnteredAt
	}
	return e.EnteredAt
}

type IncidentEventType string

const (
	IncidentEventCreated            IncidentEventType = "created"
	IncidentEventPhaseChanged       IncidentEventType = "phase_changed"
	IncidentEventPhaseSkipped       IncidentEventType = "phase_skipped"
	IncidentEventClosed             IncidentEventType = "closed"
	IncidentEventSeverityPriority   IncidentEventType = "severity_priority_changed"
	IncidentEventDescriptionEdited  IncidentEventType = "description_edited"
	IncidentEventTagsChanged        IncidentEventType = "tags_changed"
	IncidentEventAlertLinked        IncidentEventType = "alert_linked"
	IncidentEventAlertUnlinked      IncidentEventType = "alert_unlinked"
	IncidentEventAIAnalysisRun      IncidentEventType = "ai_analysis_run"
	IncidentEventTimestampCorrected IncidentEventType = "status_timestamp_corrected"
	IncidentEventAssigneesChanged   IncidentEventType = "assignees_changed"
	IncidentEventRoleAssigned       IncidentEventType = "role_assigned"
	IncidentEventRoleUnassigned     IncidentEventType = "role_unassigned"
)

// IncidentEvent is an append-only audit row — see incident_events in
// db/migrations/0001_initial_schema.up.sql. Never updated after insert.
type IncidentEvent struct {
	ID         int64             `json:"id"`
	IncidentID uuid.UUID         `json:"incidentId"`
	TenantID   uuid.UUID         `json:"tenantId"`
	EventType  IncidentEventType `json:"eventType"`
	ActorType  ActorType         `json:"actorType"`
	ActorID    *uuid.UUID        `json:"actorId,omitempty"`
	Data       json.RawMessage   `json:"data"`
	CreatedAt  time.Time         `json:"createdAt"`
}

// IncidentComment is a Team Notes entry — separate from IncidentEvent, which
// is the system+user audit trail. Comments are user-authored discussion,
// optionally with an attached file.
type IncidentComment struct {
	ID         uuid.UUID `json:"id"`
	IncidentID uuid.UUID `json:"incidentId"`
	TenantID   uuid.UUID `json:"tenantId"`
	AuthorID   uuid.UUID `json:"authorId"`
	// AuthorName is denormalized at write time (see
	// db/migrations/0001_initial_schema.up.sql) so Team Notes
	// can show who wrote a comment without a user-lookup endpoint a non-admin
	// analyst wouldn't have access to.
	AuthorName    string    `json:"authorName"`
	Body          string    `json:"body"`
	AttachmentURL *string   `json:"attachmentUrl,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}
