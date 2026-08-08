// Package service holds business rules that must not be bypassable by going
// straight to the repository — status transitions, the append-only event
// log, tag-based visibility, and what is allowed to change once an alert is
// closed.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// OnCallResolver resolves who's on shift right now -- satisfied by
// *OnCallShiftService. Defined as an interface here (rather than AlertService
// depending on the on-call schema directly) so most construction sites
// (cmd/api, every test) can leave it nil and Ingest just skips auto-assign;
// only cmd/ingest wires a real one via EnableOnCallAutoAssign.
type OnCallResolver interface {
	ResolveCurrentAnalyst(ctx context.Context, tenantID uuid.UUID, now time.Time) (*uuid.UUID, error)
}

type AlertService struct {
	pool        *db.Pool
	repo        *repository.AlertRepository
	tags        *TagService
	onCall      OnCallResolver
	publish     func(tenantID uuid.UUID, eventType string, payload any)
	autoAnalyze func(tenantID, alertID uuid.UUID)
	runs        *repository.AIAnalysisRunRepository
}

func NewAlertService(pool *db.Pool, repo *repository.AlertRepository, tags *TagService) *AlertService {
	return &AlertService{pool: pool, repo: repo, tags: tags}
}

// EnableOnCallAutoAssign wires the on-call resolver used by Ingest to
// auto-assign each newly ingested alert to whoever's on shift. Optional --
// see OnCallResolver's doc comment for why this is a post-construction
// setter rather than a constructor parameter.
func (s *AlertService) EnableOnCallAutoAssign(resolver OnCallResolver) {
	s.onCall = resolver
}

// EnableEventPublishing wires a live-update notifier (events.Broadcaster.Publish
// in practice) -- called after Ingest/ChangeStatus/OverrideSeverity commit,
// so connected SSE clients (see EventsHandlers.Stream) know to refresh
// instead of only picking up a change on the next manual reload. Optional,
// same post-construction-setter reasoning as EnableOnCallAutoAssign: most
// construction sites (cmd/ingest, every test) have no broadcaster and just
// skip publishing.
func (s *AlertService) EnableEventPublishing(publish func(tenantID uuid.UUID, eventType string, payload any)) {
	s.publish = publish
}

// EnableAutoAnalysis wires an AI-analysis trigger fired (in its own
// goroutine, never awaited -- see Ingest) the moment a webhook alert lands,
// so an analyst opening a freshly-received alert already has a triage
// summary waiting instead of needing to click "Analyze with AI" first.
// Optional, same post-construction-setter reasoning as
// EnableOnCallAutoAssign: only cmd/ingest wires a real one (via
// AIAnalysisService.AnalyzeAlert with a nil actorID, see that method's doc
// comment) -- cmd/api's own "Analyze with AI" button already goes straight
// through the handler, it doesn't need Ingest to also trigger one.
func (s *AlertService) EnableAutoAnalysis(trigger func(tenantID, alertID uuid.UUID)) {
	s.autoAnalyze = trigger
}

// EnableAnalysisLookup wires the repository Get uses to populate
// domain.Alert.LatestAnalysis. Optional, same reasoning as
// EnableOnCallAutoAssign/EnableAutoAnalysis -- most tests construct
// AlertService without it and just get a nil LatestAnalysis, which is
// exactly what "no completed analysis yet" should look like.
func (s *AlertService) EnableAnalysisLookup(runs *repository.AIAnalysisRunRepository) {
	s.runs = runs
}

func (s *AlertService) publishEvent(tenantID uuid.UUID, alertID uuid.UUID, action string) {
	if s.publish != nil {
		s.publish(tenantID, "alert", map[string]any{"id": alertID, "action": action})
	}
}

// Get returns the alert, or nil if it doesn't exist, belongs to another
// tenant (blocked by RLS), or isn't visible under allowedTags -- all three
// cases look identical to a caller, which is deliberate: a user shouldn't
// be able to distinguish "no such alert" from "exists but you can't see it"
// through response shape (see design handoff, "Tag-based + resource-based
// access scoping").
func (s *AlertService) Get(ctx context.Context, tenantID, id uuid.UUID, allowedTags []string) (*domain.Alert, error) {
	var alert *domain.Alert
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		a, err := s.repo.Get(ctx, tx, id)
		if err != nil || a == nil {
			return err
		}
		if !tagsVisible(allowedTags, a.Tags) {
			return nil
		}
		if s.runs != nil {
			result, err := s.runs.LatestCompletedResult(ctx, tx, "alert", a.ID)
			if err != nil {
				return fmt.Errorf("load latest analysis: %w", err)
			}
			a.LatestAnalysis = result
		}
		alert = a
		return nil
	})
	return alert, err
}

func (s *AlertService) List(ctx context.Context, tenantID uuid.UUID, f repository.ListAlertsFilter) ([]domain.Alert, error) {
	var alerts []domain.Alert
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		a, err := s.repo.List(ctx, tx, f)
		alerts = a
		return err
	})
	return alerts, err
}

// ChangeStatus applies an analyst-driven status transition. Closing an alert
// must go through Close instead — this rejects a direct transition to
// 'closed' because that path is the only one allowed to set classification
// (mirrors the DB check constraint alerts_closed_requires_classification).
func (s *AlertService) ChangeStatus(ctx context.Context, tenantID, alertID, actorID uuid.UUID, newStatus domain.AlertStatus, allowedTags []string) error {
	if newStatus == domain.AlertStatusClosed {
		return fmt.Errorf("use Close to transition an alert to closed, so classification is always captured")
	}

	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.repo.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if current == nil || !tagsVisible(allowedTags, current.Tags) {
			return fmt.Errorf("alert %s not found", alertID)
		}
		if current.Status == domain.AlertStatusClosed {
			return fmt.Errorf("alert %s is closed and its status is read-only", alertID)
		}

		// MTTA stamps the first time the alert leaves 'open', per the
		// design handoff's metric definition — never re-stamped afterward.
		stampAcknowledged := current.Status == domain.AlertStatusOpen

		if err := s.repo.UpdateStatus(ctx, tx, alertID, newStatus, stampAcknowledged); err != nil {
			return fmt.Errorf("update status: %w", err)
		}

		data, _ := json.Marshal(map[string]string{
			"from": string(current.Status),
			"to":   string(newStatus),
		})
		return s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
			AlertID:   alertID,
			TenantID:  tenantID,
			EventType: domain.AlertEventStatusChanged,
			ActorType: domain.ActorUser,
			ActorID:   &actorID,
			Data:      data,
		})
	})
	if err == nil {
		s.publishEvent(tenantID, alertID, "status_changed")
	}
	return err
}

// UpdateTags replaces an alert's tags with the given set, filtered down to
// whatever's actually registered in Settings -> Tags (see
// TagService.FilterKnown) -- an analyst can only attach a tag that already
// exists in the catalog, never an arbitrary free-text string.
func (s *AlertService) UpdateTags(ctx context.Context, tenantID, alertID, actorID uuid.UUID, tags []string, allowedTags []string) error {
	known, err := s.tags.FilterKnown(ctx, tenantID, tags)
	if err != nil {
		return fmt.Errorf("validate tags: %w", err)
	}
	known = orEmptySlice(known)

	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.repo.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if current == nil || !tagsVisible(allowedTags, current.Tags) {
			return fmt.Errorf("alert %s not found", alertID)
		}
		if err := s.repo.UpdateTags(ctx, tx, alertID, known); err != nil {
			return fmt.Errorf("update tags: %w", err)
		}
		data, _ := json.Marshal(map[string]any{"tags": known})
		return s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
			AlertID:   alertID,
			TenantID:  tenantID,
			EventType: domain.AlertEventTagsChanged,
			ActorType: domain.ActorUser,
			ActorID:   &actorID,
			Data:      data,
		})
	})
}

// Ingest records a newly received alert. It always starts 'open' with no
// classification, per the alert lifecycle in the design handoff — the
// severity carried on the webhook becomes both Severity and
// OriginalSeverity so a later manual override has something to diff against.
func (s *AlertService) Ingest(ctx context.Context, tenantID uuid.UUID, webhookEndpointID uuid.UUID, in domain.Alert) (*domain.Alert, error) {
	in.TenantID = tenantID
	in.WebhookEndpointID = &webhookEndpointID
	in.OriginalSeverity = in.Severity
	in.Status = domain.AlertStatusOpen
	in.Tags = orEmptySlice(in.Tags) // see orEmptySlice: alerts.tags is NOT NULL
	// received_at is stamped here, not left to the column's `default now()`
	// -- the INSERT in AlertRepository.Insert names the column explicitly
	// (it's a bound parameter, not omitted), so the DB default never fires.
	// Always server time, never source-supplied: a compromised/misconfigured
	// source claiming an arbitrary receipt time would corrupt MTTA/MTTR and
	// "recently received" ordering, both of which anchor on this field.
	in.ReceivedAt = time.Now()

	// Auto-assign to whoever's on shift, if on-call scheduling is enabled
	// (see EnableOnCallAutoAssign). Resolved before the insert tx opens --
	// it's a read-only lookup against a different table/tx, no need to
	// share a transaction with the insert below. No match (or on-call
	// disabled) just leaves the alert unassigned; it never blocks ingest.
	if s.onCall != nil {
		analystID, err := s.onCall.ResolveCurrentAnalyst(ctx, tenantID, in.ReceivedAt)
		if err != nil {
			return nil, fmt.Errorf("resolve on-call analyst: %w", err)
		}
		in.AssignedAnalystID = analystID
	}

	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.Insert(ctx, tx, &in); err != nil {
			return fmt.Errorf("insert alert: %w", err)
		}
		return s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
			AlertID:   in.ID,
			TenantID:  tenantID,
			EventType: domain.AlertEventReceived,
			ActorType: domain.ActorSystem,
			Data:      []byte(`{"channel":"webhook"}`),
		})
	})
	if err != nil {
		return nil, err
	}
	s.publishEvent(tenantID, in.ID, "received")

	// Fire-and-forget: an LLM call (possibly an agentic tool-use loop) can
	// take several seconds, and this is the webhook ingest path -- the
	// source SIEM/XDR tool is waiting on this HTTP response, it must never
	// block on analysis. context.Background() deliberately, not ctx: by
	// the time the goroutine runs, the request that triggered Ingest may
	// already have returned and had its context cancelled.
	if s.autoAnalyze != nil {
		alertID := in.ID
		go s.autoAnalyze(tenantID, alertID)
	}

	return &in, nil
}

// OverrideSeverity is the manual "Override Severity" action. Like
// ChangeStatus, it's blocked once the alert is closed — severity becomes
// read-only history at that point, same as status. original_severity is left
// untouched (see AlertRepository.UpdateSeverity), so the override is always
// visible as a delta against what the source actually sent.
func (s *AlertService) OverrideSeverity(ctx context.Context, tenantID, alertID, actorID uuid.UUID, newSeverity domain.Severity, allowedTags []string) error {
	changed := false
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.repo.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if current == nil || !tagsVisible(allowedTags, current.Tags) {
			return fmt.Errorf("alert %s not found", alertID)
		}
		if current.Status == domain.AlertStatusClosed {
			return fmt.Errorf("alert %s is closed and its severity is read-only", alertID)
		}
		if current.Severity == newSeverity {
			return nil
		}

		if err := s.repo.UpdateSeverity(ctx, tx, alertID, newSeverity); err != nil {
			return fmt.Errorf("update severity: %w", err)
		}

		data, _ := json.Marshal(map[string]string{
			"from": string(current.Severity),
			"to":   string(newSeverity),
		})
		if err := s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
			AlertID:   alertID,
			TenantID:  tenantID,
			EventType: domain.AlertEventSeverityChanged,
			ActorType: domain.ActorUser,
			ActorID:   &actorID,
			Data:      data,
		}); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err == nil && changed {
		s.publishEvent(tenantID, alertID, "severity_changed")
	}
	return err
}

// Reassign sets or clears (analystID nil) the alert's assigned analyst.
// analystID isn't validated against the user directory here -- the
// assigned_analyst_id foreign key already rejects an unknown user at the DB
// level, same trust boundary as OverrideSeverity's newSeverity.
func (s *AlertService) Reassign(ctx context.Context, tenantID, alertID, actorID uuid.UUID, analystID *uuid.UUID, allowedTags []string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.repo.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if current == nil || !tagsVisible(allowedTags, current.Tags) {
			return fmt.Errorf("alert %s not found", alertID)
		}
		if uuidEqual(current.AssignedAnalystID, analystID) {
			return nil
		}

		if err := s.repo.UpdateAssignee(ctx, tx, alertID, analystID); err != nil {
			return fmt.Errorf("update assignee: %w", err)
		}

		data, _ := json.Marshal(map[string]*uuid.UUID{
			"from": current.AssignedAnalystID,
			"to":   analystID,
		})
		return s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
			AlertID:   alertID,
			TenantID:  tenantID,
			EventType: domain.AlertEventAssigneeChanged,
			ActorType: domain.ActorUser,
			ActorID:   &actorID,
			Data:      data,
		})
	})
}

// uuidEqual compares two possibly-nil UUID pointers by value.
func uuidEqual(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// LinkAlert records a manual correlation between two alerts that weren't
// automatically merged into the same incident. Both alerts must be visible
// under allowedTags -- linking is itself a way to learn about an alert's
// existence, so the tag-visibility guard has to cover otherID too, not just
// alertID.
func (s *AlertService) LinkAlert(ctx context.Context, tenantID, alertID, otherID, actorID uuid.UUID, allowedTags []string) error {
	if alertID == otherID {
		return fmt.Errorf("cannot link an alert to itself")
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.repo.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if current == nil || !tagsVisible(allowedTags, current.Tags) {
			return fmt.Errorf("alert %s not found", alertID)
		}
		other, err := s.repo.Get(ctx, tx, otherID)
		if err != nil {
			return fmt.Errorf("load linked alert: %w", err)
		}
		if other == nil || !tagsVisible(allowedTags, other.Tags) {
			return fmt.Errorf("alert %s not found", otherID)
		}

		if err := s.repo.LinkAlert(ctx, tx, alertID, otherID, tenantID, actorID); err != nil {
			return fmt.Errorf("link alert: %w", err)
		}

		data, _ := json.Marshal(map[string]string{"alertId": otherID.String()})
		return s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
			AlertID:   alertID,
			TenantID:  tenantID,
			EventType: domain.AlertEventLinked,
			ActorType: domain.ActorUser,
			ActorID:   &actorID,
			Data:      data,
		})
	})
}

func (s *AlertService) UnlinkAlert(ctx context.Context, tenantID, alertID, otherID uuid.UUID, allowedTags []string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.repo.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if current == nil || !tagsVisible(allowedTags, current.Tags) {
			return fmt.Errorf("alert %s not found", alertID)
		}
		return s.repo.UnlinkAlert(ctx, tx, alertID, otherID)
	})
}

func (s *AlertService) LinkedAlerts(ctx context.Context, tenantID, alertID uuid.UUID, allowedTags []string) ([]domain.Alert, error) {
	var linked []domain.Alert
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.repo.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if current == nil || !tagsVisible(allowedTags, current.Tags) {
			return fmt.Errorf("alert %s not found", alertID)
		}
		all, err := s.repo.ListLinkedAlerts(ctx, tx, alertID)
		if err != nil {
			return err
		}
		for _, a := range all {
			if tagsVisible(allowedTags, a.Tags) {
				linked = append(linked, a)
			}
		}
		return nil
	})
	return linked, err
}

// Close is the only path that sets classification, matching the prototype's
// "Close & Classify Alert" flow — status, severity, and classification all
// become read-only in the API once this runs, enforced both here and by the
// alerts_classification_requires_closed / alerts_closed_requires_classification
// check constraints in the database as a second line of defense.
func (s *AlertService) Close(ctx context.Context, tenantID, alertID, actorID uuid.UUID, in domain.CloseAlertInput, allowedTags []string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.repo.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if current == nil || !tagsVisible(allowedTags, current.Tags) {
			return fmt.Errorf("alert %s not found", alertID)
		}
		if current.Status == domain.AlertStatusClosed {
			return fmt.Errorf("alert %s is already closed", alertID)
		}

		if err := s.repo.Close(ctx, tx, alertID, in); err != nil {
			return fmt.Errorf("close alert: %w", err)
		}

		data, _ := json.Marshal(map[string]any{
			"classification": in.Classification,
			"hasComment":     in.Comment != "",
			"hasImage":       in.ImageURL != nil,
		})
		return s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
			AlertID:   alertID,
			TenantID:  tenantID,
			EventType: domain.AlertEventClosed,
			ActorType: domain.ActorUser,
			ActorID:   &actorID,
			Data:      data,
		})
	})
}

// AddComment/Comments back Team Notes on an alert -- same shape as
// IncidentService.AddComment/Comments. Unlike the mutating methods above,
// this doesn't repeat the tag-visibility check (see the equivalent note on
// IncidentService's sub-resource methods) -- relies on tenant RLS alone.
func (s *AlertService) AddComment(ctx context.Context, tenantID, alertID, authorID uuid.UUID, authorName, body string, imageURL *string) (*domain.AlertComment, error) {
	c := &domain.AlertComment{
		AlertID:    alertID,
		TenantID:   tenantID,
		AuthorID:   authorID,
		AuthorName: authorName,
		Body:       body,
		ImageURL:   imageURL,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.InsertComment(ctx, tx, c)
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *AlertService) Comments(ctx context.Context, tenantID, alertID uuid.UUID) ([]domain.AlertComment, error) {
	var comments []domain.AlertComment
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.ListComments(ctx, tx, alertID)
		comments = v
		return err
	})
	return comments, err
}
