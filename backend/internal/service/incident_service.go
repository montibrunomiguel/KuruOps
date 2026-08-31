package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
)

type IncidentService struct {
	pool    *db.Pool
	repo    *repository.IncidentRepository
	tags    *TagService
	users   *repository.UserRepository
	sla     *IncidentSLAService
	publish func(tenantID uuid.UUID, eventType string, payload any)
	runs    *repository.AIAnalysisRunRepository
}

func NewIncidentService(pool *db.Pool, repo *repository.IncidentRepository, tags *TagService, users *repository.UserRepository, sla *IncidentSLAService) *IncidentService {
	return &IncidentService{pool: pool, repo: repo, tags: tags, users: users, sla: sla}
}

// EnableEventPublishing wires a live-update notifier (events.Broadcaster.Publish
// in practice) -- see AlertService.EnableEventPublishing for the same
// post-construction-setter reasoning.
func (s *IncidentService) EnableEventPublishing(publish func(tenantID uuid.UUID, eventType string, payload any)) {
	s.publish = publish
}

// EnableAnalysisLookup wires the repository Get uses to populate
// domain.Incident.LatestAnalysis/LatestAnalysisStatus/LatestAnalysisError --
// see AlertService.EnableAnalysisLookup for the same post-construction-setter
// reasoning.
func (s *IncidentService) EnableAnalysisLookup(runs *repository.AIAnalysisRunRepository) {
	s.runs = runs
}

func (s *IncidentService) publishEvent(tenantID, incidentID uuid.UUID, action string) {
	if s.publish != nil {
		s.publish(tenantID, "incident", map[string]any{"id": incidentID, "action": action})
	}
}

// resolveAssignees validates every id in userIDs against the active-user
// directory and returns the matching UserSummary rows, in no particular
// order. Unlike ingest's silent tag-drop, an unknown/inactive analyst id is
// a real user mistake worth surfacing immediately -- the whole call is
// rejected rather than silently dropping the bad id (see Create/SetAssignees).
func (s *IncidentService) resolveAssignees(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, userIDs []uuid.UUID) ([]domain.UserSummary, error) {
	summaries := make([]domain.UserSummary, 0, len(userIDs))
	for _, id := range userIDs {
		u, err := s.users.Get(ctx, tx, tenantID, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("analyst %s not found", id)
			}
			return nil, fmt.Errorf("load analyst %s: %w", id, err)
		}
		if u == nil || !u.IsActive {
			return nil, fmt.Errorf("analyst %s not found", id)
		}
		summaries = append(summaries, domain.UserSummary{ID: u.ID, Name: u.Name})
	}
	return summaries, nil
}

// Get returns the incident, or nil if it doesn't exist, belongs to another
// tenant, or isn't visible under allowedTags -- see AlertService.Get for
// why these cases are deliberately indistinguishable to the caller.
func (s *IncidentService) Get(ctx context.Context, tenantID, id uuid.UUID, allowedTags []string) (*domain.Incident, error) {
	var inc *domain.Incident
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.Get(ctx, tx, id)
		if err != nil || v == nil {
			return err
		}
		if !tagsVisible(allowedTags, v.Tags) {
			return nil
		}
		if s.runs != nil {
			run, err := s.runs.LatestRun(ctx, tx, "incident", v.ID)
			if err != nil {
				return fmt.Errorf("load latest analysis: %w", err)
			}
			v.LatestAnalysis, v.LatestAnalysisStatus, v.LatestAnalysisError = latestAnalysisFields(run)
		}
		inc = v
		return nil
	})
	return inc, err
}

// loadVisible loads id inside tx and returns it only if it exists and is
// visible under allowedTags -- nil, nil for either case, same reasoning as
// AlertService.loadVisible. Every mutating method below calls this instead
// of repeating the load-then-check block by hand.
func (s *IncidentService) loadVisible(ctx context.Context, tx pgx.Tx, id uuid.UUID, allowedTags []string) (*domain.Incident, error) {
	v, err := s.repo.Get(ctx, tx, id)
	if err != nil {
		return nil, fmt.Errorf("load incident: %w", err)
	}
	if v == nil || !tagsVisible(allowedTags, v.Tags) {
		return nil, nil
	}
	return v, nil
}

func (s *IncidentService) List(ctx context.Context, tenantID uuid.UUID, f repository.ListIncidentsFilter) ([]domain.Incident, error) {
	var incidents []domain.Incident
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx, f)
		incidents = v
		return err
	})
	return incidents, err
}

// Count mirrors List but returns the total matching row count, ignoring
// f.Limit/f.Offset -- see IncidentRepository.Count's doc comment.
func (s *IncidentService) Count(ctx context.Context, tenantID uuid.UUID, f repository.ListIncidentsFilter) (int, error) {
	var count int
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.repo.Count(ctx, tx, f)
		count = c
		return err
	})
	return count, err
}

// Create opens a new incident in the 'new' phase and records its first
// status_history entry, matching "+ New Incident" in the design handoff.
func (s *IncidentService) Create(ctx context.Context, tenantID, actorID uuid.UUID, in domain.CreateIncidentInput) (*domain.Incident, error) {
	// Same catalog rule as UpdateTags -- a tag can only be attached at
	// creation if it already exists in Settings -> Tags.
	knownTags, err := s.tags.FilterKnown(ctx, tenantID, in.Tags)
	if err != nil {
		return nil, fmt.Errorf("validate tags: %w", err)
	}

	inc := &domain.Incident{
		TenantID:    tenantID,
		Title:       in.Title,
		Description: in.Description,
		Severity:    in.Severity,
		Priority:    in.Priority,
		Phase:       domain.PhaseNew,
		Tags:        orEmptySlice(knownTags), // incidents.tags is NOT NULL — see orEmptySlice
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		assignees, err := s.resolveAssignees(ctx, tx, tenantID, in.AssigneeIDs)
		if err != nil {
			return err
		}
		dueAt, err := s.sla.DueAt(ctx, tx, inc.Severity, inc.Priority)
		if err != nil {
			return err
		}
		inc.SLADueAt = dueAt
		if err := s.repo.Insert(ctx, tx, inc); err != nil {
			return fmt.Errorf("insert incident: %w", err)
		}
		if err := s.repo.SetAssignees(ctx, tx, inc.ID, tenantID, in.AssigneeIDs); err != nil {
			return fmt.Errorf("set assignees: %w", err)
		}
		inc.Assignees = assignees
		if _, err := s.repo.RecordPhaseEntered(ctx, tx, inc.ID, tenantID, domain.PhaseNew); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]string{"title": inc.Title})
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: inc.ID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventCreated,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		})
	})
	if err != nil {
		return nil, err
	}
	s.publishEvent(tenantID, inc.ID, "created")
	return inc, nil
}

// ChangePhase moves the incident to newPhase. Matches the prototype: phase
// pills allow a direct jump to any phase, no forced linear order. If the
// jump skips one or more phases forward, an 'phase_skipped' event is logged
// alongside the normal 'phase_changed' one — visibility for the "may want to
// enforce or warn on skipping" note in the design handoff, without actually
// blocking the analyst. "Close Incident" is ChangePhase(..., PhasePostIncident).
func (s *IncidentService) ChangePhase(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, newPhase domain.IncidentPhase, allowedTags []string) error {
	changed := false
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		if current.Phase == newPhase {
			return nil
		}

		if err := s.repo.UpdatePhase(ctx, tx, incidentID, newPhase); err != nil {
			return fmt.Errorf("update phase: %w", err)
		}
		if _, err := s.repo.RecordPhaseEntered(ctx, tx, incidentID, tenantID, newPhase); err != nil {
			return err
		}

		data, _ := json.Marshal(map[string]string{
			"from": string(current.Phase),
			"to":   string(newPhase),
		})
		if err := s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventPhaseChanged,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		}); err != nil {
			return err
		}

		if isForwardSkip(current.Phase, newPhase) {
			if err := s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
				IncidentID: incidentID,
				TenantID:   tenantID,
				EventType:  domain.IncidentEventPhaseSkipped,
				ActorType:  domain.ActorSystem,
				Data:       data,
			}); err != nil {
				return err
			}
		}
		changed = true
		return nil
	})
	if err == nil && changed {
		s.publishEvent(tenantID, incidentID, "phase_changed")
	}
	return err
}

// BulkChangePhase applies ChangePhase to each of ids in turn, one
// transaction per incident -- same reasoning as AlertService.BulkChangeStatus,
// see its doc comment. Unlike ChangeStatus (which already self-rejects a
// direct transition to 'closed'), ChangePhase has no such guard for
// PhasePostIncident -- Close is a distinct method that calls ChangePhase and
// then separately stamps closed_at, so nothing inside ChangePhase itself
// stops a caller from bulk-setting a whole page of incidents to
// post_incident without ever recording a closedAt. Bulk-close was
// explicitly descoped (closing still requires the existing per-item
// classification flow via Close), so this rejects the whole request up
// front rather than letting it through and silently leaving closed_at unset
// on every affected incident.
func (s *IncidentService) BulkChangePhase(ctx context.Context, tenantID, actorID uuid.UUID, ids []uuid.UUID, newPhase domain.IncidentPhase, allowedTags []string) ([]BulkResult, error) {
	if newPhase == domain.PhasePostIncident {
		return nil, fmt.Errorf("use Close to move an incident to post-incident, so closed_at is always captured")
	}

	results := make([]BulkResult, 0, len(ids))
	for _, id := range ids {
		if err := s.ChangePhase(ctx, tenantID, id, actorID, newPhase, allowedTags); err != nil {
			results = append(results, BulkResult{ID: id, Success: false, Error: err.Error()})
		} else {
			results = append(results, BulkResult{ID: id, Success: true})
		}
	}
	return results, nil
}

// Close moves the incident to PhasePostIncident (like ChangePhase would) and
// additionally stamps closed_at — a distinct, explicit action from just
// reaching the post_incident phase via the phase tracker.
//
// These two used to be conflated: Close was literally just
// ChangePhase(..., PhasePostIncident), and IncidentRepository.UpdatePhase
// stamped closed_at itself the moment phase became post_incident. That meant
// simply clicking "Post-Incident" in the phase tracker silently closed the
// incident with no confirmation, which in turn made the frontend's "Close
// Incident" button (disabled until phase == post_incident, hidden once
// closedAt is set) permanently unreachable — the instant it would become
// enabled, it was already hidden. Closing is now only ever stamped here.
func (s *IncidentService) Close(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, allowedTags []string) error {
	if err := s.ChangePhase(ctx, tenantID, incidentID, actorID, domain.PhasePostIncident, allowedTags); err != nil {
		return err
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.MarkClosed(ctx, tx, incidentID); err != nil {
			return fmt.Errorf("mark closed: %w", err)
		}
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventClosed,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       json.RawMessage(`{}`),
		})
	})
}

func isForwardSkip(from, to domain.IncidentPhase) bool {
	fromIdx, toIdx := from.Index(), to.Index()
	if fromIdx < 0 || toIdx < 0 {
		return false
	}
	return toIdx-fromIdx > 1
}

// SetAssignees replaces an incident's full assignee set. Same
// guard/write/InsertEvent shape as SetSeverityAndPriority; every id in
// userIDs is validated against the active-user directory first, and the
// whole call is rejected if any is unknown (see resolveAssignees).
func (s *IncidentService) SetAssignees(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, userIDs []uuid.UUID, allowedTags []string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		assignees, err := s.resolveAssignees(ctx, tx, tenantID, userIDs)
		if err != nil {
			return err
		}
		if err := s.repo.SetAssignees(ctx, tx, incidentID, tenantID, userIDs); err != nil {
			return fmt.Errorf("set assignees: %w", err)
		}
		names := make([]string, len(assignees))
		for i, a := range assignees {
			names[i] = a.Name
		}
		data, _ := json.Marshal(map[string]any{"assignees": names})
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventAssigneesChanged,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		})
	})
}

// SetRole replaces role's whole assignee set on incident -- additive to
// SetAssignees, see domain.Incident.Roles's doc comment for why these are
// two separate concepts. For a single-assignee role (Commander, Technical
// Lead) userIDs must have at most one element, rejected with a clear error
// otherwise rather than letting it fail opaquely against the DB's partial
// unique index (see IncidentRepository.SetRole).
func (s *IncidentService) SetRole(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, role domain.IncidentRole, userIDs []uuid.UUID, allowedTags []string) error {
	if !role.Valid() {
		return fmt.Errorf("unknown incident role %q", role)
	}
	if role.SingleAssignee() && len(userIDs) > 1 {
		return fmt.Errorf("role %s allows at most one person", role)
	}

	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		assignees, err := s.resolveAssignees(ctx, tx, tenantID, userIDs)
		if err != nil {
			return err
		}
		if err := s.repo.SetRole(ctx, tx, incidentID, tenantID, role, userIDs); err != nil {
			return fmt.Errorf("set role: %w", err)
		}

		eventType := domain.IncidentEventRoleAssigned
		if len(assignees) == 0 {
			eventType = domain.IncidentEventRoleUnassigned
		}
		names := make([]string, len(assignees))
		for i, a := range assignees {
			names[i] = a.Name
		}
		data, _ := json.Marshal(map[string]any{"role": role, "assignees": names})
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  eventType,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		})
	})
}

// SetSeverityAndPriority is the NIST Severity x Priority matrix click —
// always sets both fields together, never one alone.
func (s *IncidentService) SetSeverityAndPriority(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, severity domain.Severity, priority domain.IncidentPriority, allowedTags []string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		dueAt, err := s.sla.DueAt(ctx, tx, severity, priority)
		if err != nil {
			return err
		}
		if err := s.repo.SetSeverityAndPriority(ctx, tx, incidentID, severity, priority, dueAt); err != nil {
			return fmt.Errorf("set severity/priority: %w", err)
		}
		data, _ := json.Marshal(map[string]string{"severity": string(severity), "priority": string(priority)})
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventSeverityPriority,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		})
	})
}

func (s *IncidentService) UpdateDescription(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, description string, allowedTags []string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		if err := s.repo.UpdateDescription(ctx, tx, incidentID, description); err != nil {
			return fmt.Errorf("update description: %w", err)
		}
		data, _ := json.Marshal(map[string]string{"description": description})
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventDescriptionEdited,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		})
	})
}

// UpdateTags replaces an incident's tags with the given set, filtered down
// to whatever's actually registered in Settings -> Tags -- see
// AlertService.UpdateTags for the same rule on alerts.
func (s *IncidentService) UpdateTags(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, tags []string, allowedTags []string) error {
	known, err := s.tags.FilterKnown(ctx, tenantID, tags)
	if err != nil {
		return fmt.Errorf("validate tags: %w", err)
	}
	known = orEmptySlice(known)

	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		if err := s.repo.UpdateTags(ctx, tx, incidentID, known); err != nil {
			return fmt.Errorf("update tags: %w", err)
		}
		data, _ := json.Marshal(map[string]any{"tags": known})
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventTagsChanged,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		})
	})
}

// CorrectPhaseTimestamp is the audit-safe replacement for freely editing a
// status_history entry's entered_at (see the schema comment in
// db/migrations/0001_initial_schema.up.sql). The original entered_at is never
// touched; this records what it should read as, who changed it, and why.
func (s *IncidentService) CorrectPhaseTimestamp(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, phase domain.IncidentPhase, correctedEnteredAt time.Time, reason string, allowedTags []string) error {
	if reason == "" {
		return fmt.Errorf("a correction reason is required")
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if inc == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		if err := s.repo.CorrectPhaseTimestamp(ctx, tx, incidentID, phase, correctedEnteredAt, actorID, reason); err != nil {
			return fmt.Errorf("correct phase timestamp: %w", err)
		}
		data, _ := json.Marshal(map[string]string{
			"phase":              string(phase),
			"reason":             reason,
			"correctedEnteredAt": correctedEnteredAt.Format(time.RFC3339),
		})
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventTimestampCorrected,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		})
	})
}

// Every sub-resource method below (StatusHistory, Timeline, Comments,
// AddComment, IOCs, AddIOC, LinkAlert, UnlinkAlert, LinkedAlerts,
// CorrectPhaseTimestamp) re-checks tag visibility via loadVisible before
// touching its own rows, rather than assuming the caller already went
// through Get. This closes the gap the previous version of this comment
// flagged and deferred: "a client which already knows an out-of-scope
// incident's ID could still reach these endpoints directly without going
// through Get first" -- which was directly exploitable over HTTP, since
// nothing forces a client to call GET /incidents/{id} before
// GET /incidents/{id}/comments. A tag-restricted analyst could read (and
// write) the comments, IOCs, timeline and linked alerts of an incident
// they cannot see, and approve its side-effecting MCP tool calls.
//
// Read methods return found=false (never a bare empty slice) when the
// incident isn't visible, so the handler can 404 exactly like Get does --
// an empty 200 would tell the caller the incident exists but has no
// comments, which is itself more than they should learn.
func (s *IncidentService) StatusHistory(ctx context.Context, tenantID, incidentID uuid.UUID, allowedTags []string) ([]domain.IncidentStatusHistoryEntry, bool, error) {
	var entries []domain.IncidentStatusHistoryEntry
	found := false
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil || inc == nil {
			return err
		}
		found = true
		v, err := s.repo.ListStatusHistory(ctx, tx, incidentID)
		entries = v
		return err
	})
	return entries, found, err
}

func (s *IncidentService) Timeline(ctx context.Context, tenantID, incidentID uuid.UUID, allowedTags []string) ([]domain.IncidentEvent, bool, error) {
	var events []domain.IncidentEvent
	found := false
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil || inc == nil {
			return err
		}
		found = true
		v, err := s.repo.ListEvents(ctx, tx, incidentID)
		events = v
		return err
	})
	return events, found, err
}

func (s *IncidentService) AddComment(ctx context.Context, tenantID, incidentID, authorID uuid.UUID, authorName, body string, attachmentURL *string, allowedTags []string) (*domain.IncidentComment, error) {
	c := &domain.IncidentComment{
		IncidentID:    incidentID,
		TenantID:      tenantID,
		AuthorID:      authorID,
		AuthorName:    authorName,
		Body:          body,
		AttachmentURL: attachmentURL,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if inc == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		return s.repo.InsertComment(ctx, tx, c)
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *IncidentService) Comments(ctx context.Context, tenantID, incidentID uuid.UUID, allowedTags []string) ([]domain.IncidentComment, bool, error) {
	var comments []domain.IncidentComment
	found := false
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil || inc == nil {
			return err
		}
		found = true
		v, err := s.repo.ListComments(ctx, tx, incidentID)
		comments = v
		return err
	})
	return comments, found, err
}

// AddIOC records a new Indicator of Compromise against incidentID -- see
// domain.IOC's doc comment for why this is append-only (no update/delete)
// and domain.IOCTypeIsValid's for why type validation lives here instead
// of a DB check constraint.
func (s *IncidentService) AddIOC(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, actorName string, iocType domain.IOCType, value, description string, identifiedAt time.Time, allowedTags []string) (*domain.IOC, error) {
	if !domain.IOCTypeIsValid(iocType) {
		return nil, fmt.Errorf("invalid IOC type %q", iocType)
	}
	if value == "" {
		return nil, fmt.Errorf("value is required")
	}
	if identifiedAt.IsZero() {
		return nil, fmt.Errorf("identifiedAt is required")
	}

	ioc := &domain.IOC{
		IncidentID:    incidentID,
		TenantID:      tenantID,
		Type:          iocType,
		Value:         value,
		Description:   description,
		IdentifiedAt:  identifiedAt,
		CreatedBy:     actorID,
		CreatedByName: actorName,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if inc == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		return s.repo.InsertIOC(ctx, tx, ioc)
	})
	if err != nil {
		return nil, err
	}
	return ioc, nil
}

func (s *IncidentService) IOCs(ctx context.Context, tenantID, incidentID uuid.UUID, allowedTags []string) ([]domain.IOC, bool, error) {
	var iocs []domain.IOC
	found := false
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil || inc == nil {
			return err
		}
		found = true
		v, err := s.repo.ListIOCs(ctx, tx, incidentID)
		iocs = v
		return err
	})
	return iocs, found, err
}

func (s *IncidentService) LinkAlert(ctx context.Context, tenantID, incidentID, alertID, actorID uuid.UUID, allowedTags []string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if inc == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		if err := s.repo.LinkAlert(ctx, tx, incidentID, alertID, tenantID); err != nil {
			return fmt.Errorf("link alert: %w", err)
		}
		data, _ := json.Marshal(map[string]string{"alertId": alertID.String()})
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventAlertLinked,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		})
	})
}

func (s *IncidentService) UnlinkAlert(ctx context.Context, tenantID, incidentID, alertID, actorID uuid.UUID, allowedTags []string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil {
			return err
		}
		if inc == nil {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		if err := s.repo.UnlinkAlert(ctx, tx, incidentID, alertID); err != nil {
			return fmt.Errorf("unlink alert: %w", err)
		}
		data, _ := json.Marshal(map[string]string{"alertId": alertID.String()})
		return s.repo.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: incidentID,
			TenantID:   tenantID,
			EventType:  domain.IncidentEventAlertUnlinked,
			ActorType:  domain.ActorUser,
			ActorID:    &actorID,
			Data:       data,
		})
	})
}

func (s *IncidentService) LinkedAlerts(ctx context.Context, tenantID, incidentID uuid.UUID, allowedTags []string) ([]domain.Alert, bool, error) {
	var alerts []domain.Alert
	found := false
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.loadVisible(ctx, tx, incidentID, allowedTags)
		if err != nil || inc == nil {
			return err
		}
		found = true
		v, err := s.repo.ListLinkedAlerts(ctx, tx, incidentID)
		alerts = v
		return err
	})
	return alerts, found, err
}
