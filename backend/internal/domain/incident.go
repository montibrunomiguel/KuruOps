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
	Assignees   []UserSummary `json:"assignees"`
	Tags        []string      `json:"tags"`
	SLADueAt    *time.Time    `json:"slaDueAt,omitempty"`
	SLABreached bool          `json:"slaBreached"`
	OpenedAt    time.Time     `json:"openedAt"`
	ClosedAt    *time.Time    `json:"closedAt,omitempty"`
	CreatedAt   time.Time     `json:"createdAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
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
// value stays intact for audit (see db/migrations/0005_incidents.up.sql).
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
)

// IncidentEvent is an append-only audit row — see incident_events in
// db/migrations/0005_incidents.up.sql. Never updated after insert.
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
// optionally with an attached image.
type IncidentComment struct {
	ID         uuid.UUID `json:"id"`
	IncidentID uuid.UUID `json:"incidentId"`
	TenantID   uuid.UUID `json:"tenantId"`
	AuthorID   uuid.UUID `json:"authorId"`
	// AuthorName is denormalized at write time (see
	// db/migrations/0017_incident_comment_author_name.up.sql) so Team Notes
	// can show who wrote a comment without a user-lookup endpoint a non-admin
	// analyst wouldn't have access to.
	AuthorName string    `json:"authorName"`
	Body       string    `json:"body"`
	ImageURL   *string   `json:"imageUrl,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}
