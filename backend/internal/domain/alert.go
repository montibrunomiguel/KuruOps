package domain

import (
	"encoding/json"
	"net"
	"time"

	"github.com/google/uuid"
)

type Severity string

const (
	SeverityCritical      Severity = "critical"
	SeverityHigh          Severity = "high"
	SeverityMedium        Severity = "medium"
	SeverityLow           Severity = "low"
	SeverityInformational Severity = "informational"
)

type AlertStatus string

const (
	AlertStatusOpen          AlertStatus = "open"
	AlertStatusInvestigating AlertStatus = "investigating"
	AlertStatusEscalated     AlertStatus = "escalated"
	AlertStatusClosed        AlertStatus = "closed"
)

type Classification string

const (
	ClassificationFalsePositive   Classification = "false_positive"
	ClassificationTruePositive    Classification = "true_positive"
	ClassificationAuthorizedEvent Classification = "authorized_event"
)

// Alert mirrors the `alerts` table. Severity/status/classification transitions
// are only ever applied through AlertService, never by writing this struct
// straight to the repository, so the close-requires-classification rule
// (also enforced by a DB check constraint) has one place to live.
type Alert struct {
	ID                uuid.UUID       `json:"id"`
	TenantID          uuid.UUID       `json:"tenantId"`
	ExternalID        *string         `json:"externalId,omitempty"`
	WebhookEndpointID *uuid.UUID      `json:"webhookEndpointId,omitempty"`
	Title             string          `json:"title"`
	Source            string          `json:"source"`
	Severity          Severity        `json:"severity"`
	OriginalSeverity  Severity        `json:"originalSeverity"`
	Status            AlertStatus     `json:"status"`
	Classification    *Classification `json:"classification,omitempty"`
	CloseComment      *string         `json:"closeComment,omitempty"`
	CloseImageURL     *string         `json:"closeImageUrl,omitempty"`
	RuleID            *string         `json:"ruleId,omitempty"`
	Asset             *string         `json:"asset,omitempty"`
	SrcIP             net.IP          `json:"srcIp,omitempty"`
	Tags              []string        `json:"tags"`
	// json.RawMessage (not []byte) so this embeds as a JSON object/value in
	// API responses instead of getting base64-encoded -- encoding/json
	// base64s a plain []byte field regardless of its actual content.
	Payload json.RawMessage `json:"payload"`
	// Metadata is the sender's own curated key/value list (Slack channel,
	// playbook link, environment, anything they want surfaced) -- see
	// db/migrations/0032_alert_metadata.up.sql. Always an object, possibly
	// empty (`{}`), never null. Distinct from Payload, which is the
	// unfiltered raw webhook body.
	Metadata   json.RawMessage `json:"metadata"`
	IncidentID *uuid.UUID      `json:"incidentId,omitempty"`
	// AssignedAnalystID/AssignedAnalystName mirror Incident's Assignees field
	// (see Incident.Assignees's doc comment) -- a live join against users
	// rather than denormalized, since assignment is mutable and the display
	// name must always reflect the analyst's current name.
	AssignedAnalystID   *uuid.UUID `json:"assignedAnalystId,omitempty"`
	AssignedAnalystName *string    `json:"assignedAnalystName,omitempty"`
	ReceivedAt          time.Time  `json:"receivedAt"`
	AcknowledgedAt      *time.Time `json:"acknowledgedAt,omitempty"`
	ClosedAt            *time.Time `json:"closedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	// LatestAnalysis is the most recent completed "Analyze with AI" run's
	// result text (see AIAnalysisRunRepository.LatestCompletedResult), nil
	// if none has completed yet -- resolved live at Get time (AlertService.Get,
	// via the optional EnableAnalysisLookup dependency), same "not
	// denormalized" reasoning as AssignedAnalystName. Only populated on a
	// single-alert Get, not List, matching Incident.Roles' precedent.
	LatestAnalysis *string `json:"latestAnalysis,omitempty"`
}

// CloseAlert is the only way classification gets set — matches the
// prototype's "Close & Classify Alert" flow, where classification is
// unlocked exclusively by the closing action.
type CloseAlertInput struct {
	Classification Classification
	Comment        string
	ImageURL       *string
}

type AlertEventType string

const (
	AlertEventReceived        AlertEventType = "received"
	AlertEventStatusChanged   AlertEventType = "status_changed"
	AlertEventSeverityChanged AlertEventType = "severity_overridden"
	AlertEventClosed          AlertEventType = "closed"
	AlertEventEscalated       AlertEventType = "escalated"
	AlertEventTagsChanged     AlertEventType = "tags_changed"
	AlertEventLinked          AlertEventType = "linked"
	AlertEventAIAnalysisRun   AlertEventType = "ai_analysis_run"
	AlertEventAssigneeChanged AlertEventType = "assignee_changed"
)

type ActorType string

const (
	ActorUser   ActorType = "user"
	ActorSystem ActorType = "system"
	ActorAI     ActorType = "ai"
)

// AlertEvent is an append-only audit row — see alert_events in
// db/migrations/0004_alerts.up.sql. Never updated after insert.
type AlertEvent struct {
	ID        int64           `json:"id"`
	AlertID   uuid.UUID       `json:"alertId"`
	TenantID  uuid.UUID       `json:"tenantId"`
	EventType AlertEventType  `json:"eventType"`
	ActorType ActorType       `json:"actorType"`
	ActorID   *uuid.UUID      `json:"actorId,omitempty"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"createdAt"`
}

// AlertComment is a Team Notes entry for an alert — same shape and purpose
// as IncidentComment (see domain.IncidentComment): user-authored discussion
// separate from the system+user AlertEvent audit trail, optionally with an
// attached image.
type AlertComment struct {
	ID         uuid.UUID `json:"id"`
	AlertID    uuid.UUID `json:"alertId"`
	TenantID   uuid.UUID `json:"tenantId"`
	AuthorID   uuid.UUID `json:"authorId"`
	AuthorName string    `json:"authorName"`
	Body       string    `json:"body"`
	ImageURL   *string   `json:"imageUrl,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}
