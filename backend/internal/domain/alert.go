package domain

import (
	"encoding/json"
	"net"
	"strings"
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

// ValidClassifications is the closed set a close must pick from. A DB check
// constraint enforces the same list; this exists so a caller can be told
// which values are accepted instead of receiving a constraint violation --
// and so a bulk close can reject one bad value once rather than failing
// every alert in the batch with the same error.
var ValidClassifications = []Classification{
	ClassificationFalsePositive,
	ClassificationTruePositive,
	ClassificationAuthorizedEvent,
}

func ClassificationIsValid(c Classification) bool {
	for _, v := range ValidClassifications {
		if v == c {
			return true
		}
	}
	return false
}

// ClassificationNames renders the accepted values for an error message.
func ClassificationNames() string {
	names := make([]string, 0, len(ValidClassifications))
	for _, c := range ValidClassifications {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}

// Alert mirrors the `alerts` table. Severity/status/classification transitions
// are only ever applied through AlertService, never by writing this struct
// straight to the repository, so the close-requires-classification rule
// (also enforced by a DB check constraint) has one place to live.
type Alert struct {
	ID                 uuid.UUID       `json:"id"`
	TenantID           uuid.UUID       `json:"tenantId"`
	ExternalID         *string         `json:"externalId,omitempty"`
	WebhookEndpointID  *uuid.UUID      `json:"webhookEndpointId,omitempty"`
	Title              string          `json:"title"`
	Source             string          `json:"source"`
	Severity           Severity        `json:"severity"`
	OriginalSeverity   Severity        `json:"originalSeverity"`
	Status             AlertStatus     `json:"status"`
	Classification     *Classification `json:"classification,omitempty"`
	CloseComment       *string         `json:"closeComment,omitempty"`
	CloseAttachmentURL *string         `json:"closeAttachmentUrl,omitempty"`
	RuleID             *string         `json:"ruleId,omitempty"`
	Asset              *string         `json:"asset,omitempty"`
	SrcIP              net.IP          `json:"srcIp,omitempty"`
	Tags               []string        `json:"tags"`
	// json.RawMessage (not []byte) so this embeds as a JSON object/value in
	// API responses instead of getting base64-encoded -- encoding/json
	// base64s a plain []byte field regardless of its actual content.
	Payload json.RawMessage `json:"payload"`
	// Metadata is the sender's own curated key/value list (Slack channel,
	// playbook link, environment, anything they want surfaced) -- see
	// db/migrations/0001_initial_schema.up.sql. Always an object, possibly
	// empty (`{}`), never null. Distinct from Payload, which is the
	// unfiltered raw webhook body.
	Metadata   json.RawMessage `json:"metadata"`
	IncidentID *uuid.UUID      `json:"incidentId,omitempty"`
	// GroupKey is the canonical value AlertService.computeGroupKey derived
	// from this alert's Payload via its webhook endpoint's GroupByFields at
	// ingest time -- nil when dedup was off, or the payload was missing one
	// of the configured fields. Repository-internal matching detail, never
	// exposed through the API (same json:"-" treatment as
	// WebhookEndpoint.TokenHash).
	GroupKey *string `json:"-"`
	// DuplicateCount is how many subsequent payloads matched this alert's
	// GroupKey within its endpoint's dedup window and were suppressed
	// (no new alert created) rather than incrementing this instead -- see
	// AlertRepository.FindAndIncrementDuplicate. 0 means no duplicates have
	// been suppressed (the common case, including every alert from an
	// endpoint with dedup off).
	DuplicateCount int `json:"duplicateCount"`
	// PlaybookID/PlaybookTitle are the playbook auto-assigned at ingest time
	// (see AlertService.Ingest / PlaybookRepository.MatchForAlertTitle) --
	// PlaybookTitle is denormalized via a join, same "cheap read, mutable
	// source of truth is elsewhere" tradeoff as AssignedAnalystName below.
	// Both nil when the tenant has no matching or default playbook.
	PlaybookID    *uuid.UUID `json:"playbookId,omitempty"`
	PlaybookTitle *string    `json:"playbookTitle,omitempty"`
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
	// LatestAnalysis/LatestAnalysisStatus/LatestAnalysisError describe the
	// single most recent "Analyze with AI" run (see
	// AIAnalysisRunRepository.LatestRun), resolved live at Get time
	// (AlertService.Get, via the optional EnableAnalysisLookup dependency),
	// same "not denormalized" reasoning as AssignedAnalystName. Only
	// populated on a single-alert Get, not List, matching Incident.Roles'
	// precedent.
	//
	// Analysis runs in the background (see AIAnalysisService.
	// StartAlertAnalysis) -- the frontend has no other way to learn a
	// requested analysis finished than reloading and reading these fields,
	// which is why AlertDetailPage refetches on the SSE "alert" event
	// AIAnalysisService fires when a run completes or fails, instead of
	// waiting on the POST /analyze response the way it used to.
	//
	// LatestAnalysisStatus is "running" | "paused" | "completed" | "failed",
	// nil if no analysis has ever been requested. LatestAnalysis (the result
	// text) is only ever set when status is "completed"; LatestAnalysisError
	// only when status is "failed".
	LatestAnalysis       *string `json:"latestAnalysis,omitempty"`
	LatestAnalysisStatus *string `json:"latestAnalysisStatus,omitempty"`
	LatestAnalysisError  *string `json:"latestAnalysisError,omitempty"`
}

// CloseAlert is the only way classification gets set — matches the
// prototype's "Close & Classify Alert" flow, where classification is
// unlocked exclusively by the closing action.
type CloseAlertInput struct {
	Classification Classification
	Comment        string
	AttachmentURL  *string
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
	// AlertEventDuplicateSuppressed is recorded on the ORIGINAL alert (never
	// on a new row -- none is created) each time a subsequent payload
	// matches its GroupKey within the endpoint's dedup window. See
	// AlertService.Ingest.
	AlertEventDuplicateSuppressed AlertEventType = "duplicate_suppressed"
	// AlertEventPlaybookWebhookTriggered records every attempt (success or
	// failure) to fire a playbook containment step's webhook against this
	// alert -- see PlaybookService.TriggerStepWebhook. Data holds
	// {"playbookId":..., "stepId":..., "success": bool}.
	AlertEventPlaybookWebhookTriggered AlertEventType = "playbook_webhook_triggered"
)

type ActorType string

const (
	ActorUser   ActorType = "user"
	ActorSystem ActorType = "system"
	ActorAI     ActorType = "ai"
)

// AlertEvent is an append-only audit row — see alert_events in
// db/migrations/0001_initial_schema.up.sql. Never updated after insert.
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
// attached file.
type AlertComment struct {
	ID            uuid.UUID `json:"id"`
	AlertID       uuid.UUID `json:"alertId"`
	TenantID      uuid.UUID `json:"tenantId"`
	AuthorID      uuid.UUID `json:"authorId"`
	AuthorName    string    `json:"authorName"`
	Body          string    `json:"body"`
	AttachmentURL *string   `json:"attachmentUrl,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}
