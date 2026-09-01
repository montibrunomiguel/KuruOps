// Package service holds business rules that must not be bypassable by going
// straight to the repository — status transitions, the append-only event
// log, tag-based visibility, and what is allowed to change once an alert is
// closed.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/jsonpath"
	"github.com/kuruops/kuruops/internal/notifier"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/safego"
)

// maxConcurrentAutoAnalysis bounds how many auto-analysis goroutines
// (Ingest's "go s.autoAnalyze(...)" launch, see below) can be in flight at
// once. Ingest's webhook path can receive a burst of alerts far faster than
// an LLM call completes, and without a cap each one spawns its own
// unbounded goroutine -- a large enough burst turns into hundreds of
// concurrent outbound LLM requests, which is a cost/rate-limit/resource
// problem for this process and the tenant's LLM provider alike, not a
// correctness one (nothing here changes what gets analyzed, only how many
// analyses run at the same instant).
const maxConcurrentAutoAnalysis = 5

// errAlertNotVisible is an internal signal, never returned to a caller: it
// lets a transaction body abort (rolling back) while letting the wrapper
// translate it back into this package's (nil, nil) not-found contract.
var errAlertNotVisible = errors.New("alert not visible")

// OnCallResolver resolves who's on shift right now -- satisfied by
// *OnCallScheduleService. Defined as an interface here (rather than AlertService
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
	playbooks   *repository.PlaybookRepository
	onCall      OnCallResolver
	publish     func(tenantID uuid.UUID, eventType string, payload any)
	autoAnalyze func(tenantID, alertID uuid.UUID)
	// autoAnalyzeSem enforces maxConcurrentAutoAnalysis -- see that
	// constant's doc comment. Buffered channel used as a counting
	// semaphore: send blocks once maxConcurrentAutoAnalysis goroutines are
	// already holding a slot, receive releases one.
	autoAnalyzeSem chan struct{}
	runs           *repository.AIAnalysisRunRepository
	// incidents/escalationPolicies/appBaseURL back Escalate -- see
	// EnableEscalation's doc comment for why these are wired via a setter
	// rather than a constructor parameter.
	incidents          *IncidentService
	escalationPolicies *EscalationPolicyService
	appBaseURL         string
}

func NewAlertService(pool *db.Pool, repo *repository.AlertRepository, tags *TagService, playbooks *repository.PlaybookRepository) *AlertService {
	return &AlertService{pool: pool, repo: repo, tags: tags, playbooks: playbooks, autoAnalyzeSem: make(chan struct{}, maxConcurrentAutoAnalysis)}
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

// EnableEscalation wires Escalate's cross-aggregate dependencies --
// incidents (to create and link the promoted incident) and
// escalationPolicies + appBaseURL (to fire the alert's severity's next
// manual-escalation chain step, see fireManualEscalationStep). Optional,
// same post-construction-setter reasoning as EnableOnCallAutoAssign:
// Escalate is only ever reached via the HTTP handler layer (an
// analyst-triggered action), so only cmd/api wires this -- cmd/ingest,
// cmd/worker, and most test constructions never call Escalate and leave it
// unset.
func (s *AlertService) EnableEscalation(incidents *IncidentService, escalationPolicies *EscalationPolicyService, appBaseURL string) {
	s.incidents = incidents
	s.escalationPolicies = escalationPolicies
	s.appBaseURL = appBaseURL
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
			run, err := s.runs.LatestRun(ctx, tx, "alert", a.ID)
			if err != nil {
				return fmt.Errorf("load latest analysis: %w", err)
			}
			a.LatestAnalysis, a.LatestAnalysisStatus, a.LatestAnalysisError = latestAnalysisFields(run)
		}
		alert = a
		return nil
	})
	return alert, err
}

// loadVisible loads id inside tx and returns it only if it exists and is
// visible under allowedTags -- nil, nil for either case (not-found and
// not-visible look identical to a caller, same reasoning as Get's doc
// comment). Every mutating method below calls this instead of repeating the
// load-then-check block by hand.
func (s *AlertService) loadVisible(ctx context.Context, tx pgx.Tx, id uuid.UUID, allowedTags []string) (*domain.Alert, error) {
	a, err := s.repo.Get(ctx, tx, id)
	if err != nil {
		return nil, fmt.Errorf("load alert: %w", err)
	}
	if a == nil || !tagsVisible(allowedTags, a.Tags) {
		return nil, nil
	}
	return a, nil
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

// Count mirrors List but returns the total matching row count, ignoring
// f.Limit/f.Offset -- see AlertRepository.Count's doc comment.
func (s *AlertService) Count(ctx context.Context, tenantID uuid.UUID, f repository.ListAlertsFilter) (int, error) {
	var count int
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.repo.Count(ctx, tx, f)
		count = c
		return err
	})
	return count, err
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
		current, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("alert %s not found", alertID)
		}
		return s.changeStatusTx(ctx, tx, tenantID, actorID, current, newStatus)
	})
	if err == nil {
		s.publishEvent(tenantID, alertID, "status_changed")
	}
	return err
}

// changeStatusTx is ChangeStatus's body without the transaction or the
// visibility load -- current is the already-loaded, already-gated alert.
// Split out so Escalate can run the status flip inside the same transaction
// that creates and links the incident (see its doc comment). Does not
// publish: the caller does that after its own commit, so an SSE event never
// announces a change that then rolls back.
func (s *AlertService) changeStatusTx(ctx context.Context, tx pgx.Tx, tenantID, actorID uuid.UUID, current *domain.Alert, newStatus domain.AlertStatus) error {
	if current.Status == domain.AlertStatusClosed {
		return fmt.Errorf("alert %s is closed and its status is read-only", current.ID)
	}

	// MTTA stamps the first time the alert leaves 'open', per the
	// design handoff's metric definition — never re-stamped afterward.
	stampAcknowledged := current.Status == domain.AlertStatusOpen

	if err := s.repo.UpdateStatus(ctx, tx, current.ID, newStatus, stampAcknowledged); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	data, _ := json.Marshal(map[string]string{
		"from": string(current.Status),
		"to":   string(newStatus),
	})
	return s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
		AlertID:   current.ID,
		TenantID:  tenantID,
		EventType: domain.AlertEventStatusChanged,
		ActorType: domain.ActorUser,
		ActorID:   &actorID,
		Data:      data,
	})
}

// BulkChangeStatus applies ChangeStatus to each of ids in turn, one
// transaction per alert (same as calling ChangeStatus that many times by
// hand) -- not a single multi-row UPDATE, so every invariant ChangeStatus
// already enforces (tag-visibility via loadVisible, the "closed is
// read-only" guard, one AlertEvent per change) keeps working unmodified.
// A caller attempting to bulk-close is naturally rejected per-item by
// ChangeStatus's own "use Close instead" guard, same as it would be for a
// single alert -- no separate whole-request check needed here for that
// case. One alert failing (not found, no longer visible, already closed)
// doesn't stop the rest from being attempted.
func (s *AlertService) BulkChangeStatus(ctx context.Context, tenantID, actorID uuid.UUID, ids []uuid.UUID, newStatus domain.AlertStatus, allowedTags []string) []BulkResult {
	results := make([]BulkResult, 0, len(ids))
	for _, id := range ids {
		if err := s.ChangeStatus(ctx, tenantID, id, actorID, newStatus, allowedTags); err != nil {
			results = append(results, BulkResult{ID: id, Success: false, Error: err.Error()})
		} else {
			results = append(results, BulkResult{ID: id, Success: true})
		}
	}
	return results
}

// AdvanceManualEscalation advances alertID's manual-escalation counter
// (entirely independent of the automatic SLA loop cmd/worker's
// sweepEscalations drives) and returns which 0-indexed escalation chain
// step to fire this call -- see AlertRepository.AdvanceManualEscalation and
// AlertHandlers.escalate, the only caller.
func (s *AlertService) AdvanceManualEscalation(ctx context.Context, tenantID, alertID uuid.UUID, maxSteps int) (int, error) {
	var position int
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		p, err := s.repo.AdvanceManualEscalation(ctx, tx, alertID, maxSteps)
		position = p
		return err
	})
	return position, err
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
		current, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
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
// computeGroupKey resolves each of fields against payload (via
// jsonpath.Resolve) and returns a canonical key identifying this specific
// combination of values -- or "" if fields is empty, payload isn't valid
// JSON, or ANY field is missing. A missing field deliberately does not
// resolve to some shared "absent" placeholder: two alerts that are each
// missing a different (or the same) configured field are not necessarily
// the same event, and matching on absence risks silently grouping alerts an
// admin never intended to group. "" means "don't dedup this one" -- Ingest
// treats it exactly like GroupByFields being unset.
//
// The key itself is the JSON encoding of the resolved values in field
// order (not a delimited string join) -- avoids ambiguity between e.g.
// fields ["a","bc"] with values ["1","2"] and fields ["ab","c"] with values
// ["1","2"] both joining to "1:2" or similar; encoding preserves each
// value's own type/boundaries.
func computeGroupKey(payload json.RawMessage, fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return ""
	}
	values := make([]any, len(fields))
	for i, f := range fields {
		v, ok := jsonpath.Resolve(root, f)
		if !ok {
			return ""
		}
		values[i] = v
	}
	key, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(key)
}

// Ingest records a webhook-received alert, unless it's a duplicate of one
// already open -- see groupByFields/dedupWindowMinutes (the ingesting
// endpoint's own dedup config, see domain.WebhookEndpoint). deduped=true
// means no new alert was created: the returned *domain.Alert is the
// EXISTING one, with DuplicateCount incremented, not a fresh row.
func (s *AlertService) Ingest(ctx context.Context, tenantID uuid.UUID, webhookEndpointID uuid.UUID, in domain.Alert, groupByFields []string, dedupWindowMinutes int) (alert *domain.Alert, deduped bool, err error) {
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

	groupKey := computeGroupKey(in.Payload, groupByFields)

	// Auto-assign to whoever's on shift, if on-call scheduling is enabled
	// (see EnableOnCallAutoAssign). Resolved before the insert tx opens --
	// it's a read-only lookup against a different table/tx, no need to
	// share a transaction with the insert below. No match (or on-call
	// disabled) just leaves the alert unassigned; it never blocks ingest.
	// Deliberately NOT gated on groupKey being empty: a non-empty groupKey
	// only means this endpoint has dedup configured and the payload has the
	// field, not that this particular alert IS a duplicate -- that's only
	// known once FindAndIncrementDuplicate runs below. Resolving here
	// unconditionally means a genuine duplicate wastes one read-only lookup
	// (in.AssignedAnalystID is simply never used for the early-return
	// dedup path below), but a fresh, non-duplicate alert on a dedup-enabled
	// endpoint no longer silently loses its on-call assignment.
	if s.onCall != nil {
		analystID, resolveErr := s.onCall.ResolveCurrentAnalyst(ctx, tenantID, in.ReceivedAt)
		if resolveErr != nil {
			return nil, false, fmt.Errorf("resolve on-call analyst: %w", resolveErr)
		}
		in.AssignedAnalystID = analystID
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if groupKey != "" {
			// Serializes the whole check-then-(increment-or-insert) section
			// per (endpoint, groupKey), same pg_advisory_xact_lock(hashtext(...))
			// idiom as middleware.RateLimiter -- without it, two concurrent
			// POSTs for the same group arriving before either has inserted
			// yet would BOTH find no existing match and both insert,
			// defeating dedup entirely. Released automatically at the end
			// of this transaction (commit or rollback), never needs an
			// explicit unlock.
			if _, lockErr := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtext($1))", webhookEndpointID.String()+":"+groupKey); lockErr != nil {
				return fmt.Errorf("acquire dedup lock: %w", lockErr)
			}

			existingID, newCount, found, findErr := s.repo.FindAndIncrementDuplicate(ctx, tx, webhookEndpointID, groupKey, dedupWindowMinutes)
			if findErr != nil {
				return fmt.Errorf("find duplicate: %w", findErr)
			}
			if found {
				if err := s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
					AlertID:   existingID,
					TenantID:  tenantID,
					EventType: domain.AlertEventDuplicateSuppressed,
					ActorType: domain.ActorSystem,
					Data:      json.RawMessage(fmt.Sprintf(`{"duplicateCount":%d}`, newCount)),
				}); err != nil {
					return fmt.Errorf("record duplicate event: %w", err)
				}
				alert = &domain.Alert{ID: existingID, TenantID: tenantID, DuplicateCount: newCount}
				deduped = true
				return nil
			}
			key := groupKey
			in.GroupKey = &key
		}

		// Every new alert is born with a playbook already attached: the most
		// specific alert_name_pattern match, or the tenant's is_default
		// playbook, or nil if neither exists (never blocks ingest, same as
		// the on-call auto-assign above -- a nil match is a valid outcome,
		// only a genuine query error aborts).
		if s.playbooks != nil {
			pb, matchErr := s.playbooks.MatchForAlertTitle(ctx, tx, in.Title)
			if matchErr != nil {
				return fmt.Errorf("match playbook: %w", matchErr)
			}
			if pb != nil {
				in.PlaybookID = &pb.ID
			}
		}

		if err := s.repo.Insert(ctx, tx, &in); err != nil {
			return fmt.Errorf("insert alert: %w", err)
		}
		alert = &in
		return s.repo.InsertEvent(ctx, tx, &domain.AlertEvent{
			AlertID:   in.ID,
			TenantID:  tenantID,
			EventType: domain.AlertEventReceived,
			ActorType: domain.ActorSystem,
			Data:      []byte(`{"channel":"webhook"}`),
		})
	})
	if err != nil {
		return nil, false, err
	}

	if deduped {
		s.publishEvent(tenantID, alert.ID, "duplicate_suppressed")
		return alert, true, nil
	}

	s.publishEvent(tenantID, alert.ID, "received")

	// Fire-and-forget: an LLM call (possibly an agentic tool-use loop) can
	// take several seconds, and this is the webhook ingest path -- the
	// source SIEM/XDR tool is waiting on this HTTP response, it must never
	// block on analysis. context.Background() deliberately, not ctx: by
	// the time the goroutine runs, the request that triggered Ingest may
	// already have returned and had its context cancelled. Never fires for
	// a suppressed duplicate -- there's no new content to analyze.
	//
	// The semaphore acquire below only ever blocks this background
	// goroutine, never Ingest's own caller -- Ingest already returned to
	// its HTTP response path the instant this goroutine was launched.
	if s.autoAnalyze != nil {
		alertID := alert.ID
		safego.Go("alert.autoAnalyze", func() {
			s.autoAnalyzeSem <- struct{}{}
			defer func() { <-s.autoAnalyzeSem }()
			s.autoAnalyze(tenantID, alertID)
		})
	}

	return alert, false, nil
}

// OverrideSeverity is the manual "Override Severity" action. Like
// ChangeStatus, it's blocked once the alert is closed — severity becomes
// read-only history at that point, same as status. original_severity is left
// untouched (see AlertRepository.UpdateSeverity), so the override is always
// visible as a delta against what the source actually sent.
func (s *AlertService) OverrideSeverity(ctx context.Context, tenantID, alertID, actorID uuid.UUID, newSeverity domain.Severity, allowedTags []string) error {
	changed := false
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
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
		current, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
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
		current, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("alert %s not found", alertID)
		}
		other, err := s.loadVisible(ctx, tx, otherID, allowedTags)
		if err != nil {
			return err
		}
		if other == nil {
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
		current, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("alert %s not found", alertID)
		}
		return s.repo.UnlinkAlert(ctx, tx, alertID, otherID)
	})
}

func (s *AlertService) LinkedAlerts(ctx context.Context, tenantID, alertID uuid.UUID, allowedTags []string) ([]domain.Alert, error) {
	var linked []domain.Alert
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
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
		current, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
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
			"hasAttachment":  in.AttachmentURL != nil,
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

// ErrAlreadyEscalated is what Escalate returns when the alert already has a
// linked incident -- the handler turns it into 409 Conflict. Escalating
// twice would create a second incident for the same alert, which is never
// what the analyst meant: the UI hides the button once the alert is
// escalated, but that's a client-side guard, and a retry (the natural
// response to a failed request), a double-click, or a direct API call all
// reach here regardless.
var ErrAlreadyEscalated = errors.New("this alert has already been escalated to an incident")

// Escalate creates a new incident from the alert (title/severity/tags
// copied over, priority seeded from the alert's severity via
// domain.DefaultPriorityForSeverity -- an analyst can still override it on
// the incident's NIST matrix afterward), links the alert to it, and marks
// the alert 'escalated' -- the one place that legitimately touches both
// aggregates. Requires EnableEscalation to have been called first (see its
// doc comment); returns an error otherwise, since a caller reaching this
// without wiring it is a construction bug, not a runtime condition to
// handle gracefully.
//
// All three writes share ONE transaction. They used to be three separate
// ones (incidents.Create, incidents.LinkAlert, then ChangeStatus), which
// left real partial states on any mid-sequence failure: a link failure
// stranded an incident that was created but never attached to anything,
// and a status failure left the alert looking un-escalated while already
// carrying an incident. Both then invited a retry, and because the create
// ran unconditionally, each retry minted another incident for the same
// alert. The ErrAlreadyEscalated guard below closes that second half.
func (s *AlertService) Escalate(ctx context.Context, tenantID, actorID, alertID uuid.UUID, allowedTags []string) (*domain.Incident, error) {
	if s.incidents == nil {
		return nil, fmt.Errorf("escalation is not enabled on this AlertService instance")
	}

	var (
		incident *domain.Incident
		// Captured inside the tx for the post-commit background step
		// below -- the loaded alert itself doesn't outlive the closure.
		severity domain.Severity
		title    string
	)
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		alert, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if alert == nil {
			return errAlertNotVisible
		}
		// Alert.IncidentID is derived from incident_alert_links (see
		// AlertRepository's column comment), so this is the real "is there
		// already an incident for this alert" answer, not a denormalized
		// flag that could drift.
		if alert.IncidentID != nil {
			return ErrAlreadyEscalated
		}

		knownTags, err := s.tags.filterKnownTx(ctx, tx, alert.Tags)
		if err != nil {
			return fmt.Errorf("validate tags: %w", err)
		}

		inc, err := s.incidents.createTx(ctx, tx, tenantID, actorID, domain.CreateIncidentInput{
			Title:    alert.Title,
			Severity: alert.Severity,
			Priority: domain.DefaultPriorityForSeverity(alert.Severity),
			Tags:     alert.Tags,
		}, knownTags)
		if err != nil {
			return err
		}
		if err := s.incidents.linkAlertTx(ctx, tx, tenantID, inc.ID, alert.ID, actorID); err != nil {
			return err
		}
		if err := s.changeStatusTx(ctx, tx, tenantID, actorID, alert, domain.AlertStatusEscalated); err != nil {
			return err
		}
		incident = inc
		severity = alert.Severity
		title = alert.Title
		return nil
	})
	if err != nil {
		if errors.Is(err, errAlertNotVisible) {
			// Same (nil, nil) not-found contract this method always had --
			// the handler turns it into 404.
			return nil, nil
		}
		return nil, err
	}

	// Published only after the commit, so neither event can announce work
	// that rolled back.
	s.incidents.publishEvent(tenantID, incident.ID, "created")
	s.publishEvent(tenantID, alertID, "status_changed")

	// Best-effort, backgrounded (same "go s.autoAnalyze(...)" pattern
	// Ingest uses) so a slow/unreachable escalation-chain destination never
	// delays this response -- see fireManualEscalationStep.
	safego.Go("alert.fireManualEscalationStep", func() {
		s.fireManualEscalationStep(context.Background(), tenantID, alertID, severity, title)
	})

	return incident, nil
}

// fireManualEscalationStep is Escalate's manual-escalation side effect: if
// the alert's severity has a configured Escala de Acionamento chain, fires
// its next step exactly once -- AlertRepository.AdvanceManualEscalation's
// counter is entirely independent of cmd/worker's automatic SLA loop (see
// alerts.manual_escalation_step), so this never wraps back to step 0 and
// never interferes with the SLA loop's own progress through the chain.
// Every failure (no chain configured, resolve/send errors) is only logged
// -- this must never surface as a failure of Escalate itself, which has
// already promoted the alert to an incident by the time this runs.
func (s *AlertService) fireManualEscalationStep(ctx context.Context, tenantID, alertID uuid.UUID, severity domain.Severity, title string) {
	policy, err := s.escalationPolicies.Get(ctx, tenantID, severity)
	if err != nil {
		slog.Warn("manual escalation step skipped: load chain failed", "alert_id", alertID, "error", err)
		return
	}
	if policy == nil || len(policy.Steps) == 0 {
		return
	}

	position, err := s.AdvanceManualEscalation(ctx, tenantID, alertID, len(policy.Steps))
	if err != nil {
		slog.Warn("manual escalation step skipped: advance counter failed", "alert_id", alertID, "error", err)
		return
	}
	step := policy.Steps[position]

	sender, err := notifier.NewForPolicy(string(step.ChannelType), step.WebhookPayloadTemplate)
	if err != nil {
		slog.Warn("manual escalation step skipped: unknown channel", "alert_id", alertID, "channel", step.ChannelType, "error", err)
		return
	}

	notification, destination, err := s.escalationPolicies.ResolveStepNotification(ctx, tenantID, step, notifier.Notification{
		Title: title, Severity: string(severity), AlertID: alertID.String(),
		URL: s.appBaseURL + "/alerts/" + alertID.String(),
	})
	if err != nil {
		slog.Warn("manual escalation step skipped: resolve notification failed", "alert_id", alertID, "error", err)
		return
	}

	if err := sender.Send(ctx, destination, notification); err != nil {
		slog.Warn("manual escalation step send failed", "alert_id", alertID, "step", position, "channel", step.ChannelType, "error", err)
		return
	}
	slog.Info("manual escalation step fired", "alert_id", alertID, "step", position, "channel", step.ChannelType)
}

// AddComment/Comments back Team Notes on an alert -- same shape as
// IncidentService.AddComment/Comments, including the tag-visibility
// re-check both now perform. They used to rely on tenant RLS alone, on the
// assumption the caller had already loaded the alert via Get; nothing
// forces an HTTP client to do that, so a tag-restricted analyst could read
// and post Team Notes on an alert they cannot see. See IncidentService's
// sub-resource note for the full reasoning.
func (s *AlertService) AddComment(ctx context.Context, tenantID, alertID, authorID uuid.UUID, authorName, body string, attachmentURL *string, allowedTags []string) (*domain.AlertComment, error) {
	c := &domain.AlertComment{
		AlertID:       alertID,
		TenantID:      tenantID,
		AuthorID:      authorID,
		AuthorName:    authorName,
		Body:          body,
		AttachmentURL: attachmentURL,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		alert, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil {
			return err
		}
		if alert == nil {
			return fmt.Errorf("alert %s not found", alertID)
		}
		return s.repo.InsertComment(ctx, tx, c)
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *AlertService) Comments(ctx context.Context, tenantID, alertID uuid.UUID, allowedTags []string) ([]domain.AlertComment, bool, error) {
	var comments []domain.AlertComment
	found := false
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		alert, err := s.loadVisible(ctx, tx, alertID, allowedTags)
		if err != nil || alert == nil {
			return err
		}
		found = true
		v, err := s.repo.ListComments(ctx, tx, alertID)
		comments = v
		return err
	})
	return comments, found, err
}
