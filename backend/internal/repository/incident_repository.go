package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

// IncidentRepository is the only place that writes SQL for incidents and
// their related tables. Same rule as AlertRepository: every method takes a
// pgx.Tx obtained from db.Pool.WithTenant, never a bare pool, so a query
// can't accidentally run without app.tenant_id set and RLS scoping it.
type IncidentRepository struct{}

func NewIncidentRepository() *IncidentRepository {
	return &IncidentRepository{}
}

const incidentColumns = `
	id, tenant_id, title, description, severity, priority, phase,
	tags, sla_due_at, sla_breached, opened_at, closed_at, created_at, updated_at`

func (r *IncidentRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Incident, error) {
	row := tx.QueryRow(ctx, `select `+incidentColumns+` from incidents where id = $1`, id)
	inc, err := scanIncident(row)
	if err != nil || inc == nil {
		return inc, err
	}
	assignees, err := r.AssigneesForIncidents(ctx, tx, []uuid.UUID{inc.ID})
	if err != nil {
		return nil, err
	}
	// assignees[inc.ID] is nil (not []domain.UserSummary{}) for a missing map
	// key -- normalize so the JSON response is always [], never null (see
	// domain.Incident.Assignees's json tag, which has no omitempty).
	inc.Assignees = orEmptyUserSummarySlice(assignees[inc.ID])

	roles, err := r.RolesForIncident(ctx, tx, inc.ID)
	if err != nil {
		return nil, err
	}
	inc.Roles = roles
	return inc, nil
}

type ListIncidentsFilter struct {
	Severity *domain.Severity
	// Severities is an OR'd alternative to Severity, for the Incidents list
	// page's now-multi-select severity filter -- set at most one of the
	// pair; if both are set, Severity wins (Severities is ignored), same
	// precedence ListAlertsFilter.Severity/Severities already uses.
	Severities  []domain.Severity
	Priority    *domain.IncidentPriority
	Phase       *domain.IncidentPhase
	// Phases is an OR'd alternative to Phase, for the Incidents list page's
	// now-multi-select "status" filter (which is really NIST phase -- see
	// IncidentsListPage). Same set-at-most-one-of-the-pair precedence.
	Phases      []domain.IncidentPhase
	SLABreached *bool
	Tag         *string
	// OpenedSince/OpenedUntil restrict to incidents opened within
	// [OpenedSince, OpenedUntil] -- the Dashboard's time-range filter (see
	// repository.StatsFilter.Since/Until), either end optional.
	OpenedSince *time.Time
	OpenedUntil *time.Time
	// CommanderID restricts to incidents where this user holds the
	// 'commander' role -- the Dashboard Incidents tab's commander filter
	// (see repository.StatsFilter.CommanderID).
	CommanderID *uuid.UUID
	// Q full-text-matches against title/description (see the generated
	// search_vector column, db/migrations/0008_fulltext_search).
	Q *string
	// AllowedTags scopes results to the caller's tag-based access -- see
	// the identical field on ListAlertsFilter for the full explanation.
	AllowedTags []string
	Limit       int
	Offset      int
}

// incidentWhereClause builds the "where ..." fragment (starting with "where
// 1 = 1" so every branch below can unconditionally prepend "and") plus its
// positional args, shared by List and Count so the two can never drift apart
// on which rows they consider a match.
func incidentWhereClause(f ListIncidentsFilter) (string, []any) {
	query := " where 1 = 1"
	args := []any{}

	if f.Severity != nil {
		args = append(args, *f.Severity)
		query += fmt.Sprintf(" and severity = $%d", len(args))
	} else if len(f.Severities) > 0 {
		severities := make([]string, len(f.Severities))
		for i, s := range f.Severities {
			severities[i] = string(s)
		}
		args = append(args, severities)
		query += fmt.Sprintf(" and severity = any($%d::severity_enum[])", len(args))
	}
	if f.Priority != nil {
		args = append(args, *f.Priority)
		query += fmt.Sprintf(" and priority = $%d", len(args))
	}
	if f.Phase != nil {
		args = append(args, *f.Phase)
		query += fmt.Sprintf(" and phase = $%d", len(args))
	} else if len(f.Phases) > 0 {
		phases := make([]string, len(f.Phases))
		for i, p := range f.Phases {
			phases[i] = string(p)
		}
		args = append(args, phases)
		query += fmt.Sprintf(" and phase = any($%d::incident_phase_enum[])", len(args))
	}
	if f.SLABreached != nil {
		args = append(args, *f.SLABreached)
		query += fmt.Sprintf(" and sla_breached = $%d", len(args))
	}
	if f.Tag != nil {
		args = append(args, *f.Tag)
		query += fmt.Sprintf(" and $%d = any(tags)", len(args))
	}
	if f.OpenedSince != nil {
		args = append(args, *f.OpenedSince)
		query += fmt.Sprintf(" and opened_at >= $%d", len(args))
	}
	if f.OpenedUntil != nil {
		args = append(args, *f.OpenedUntil)
		query += fmt.Sprintf(" and opened_at <= $%d", len(args))
	}
	if f.CommanderID != nil {
		args = append(args, *f.CommanderID)
		query += fmt.Sprintf(
			" and id in (select incident_id from incident_role_assignments where role = 'commander' and user_id = $%d)",
			len(args),
		)
	}
	if len(f.AllowedTags) > 0 {
		args = append(args, f.AllowedTags)
		query += fmt.Sprintf(" and tags && $%d", len(args))
	}
	if f.Q != nil {
		args = append(args, *f.Q)
		query += fmt.Sprintf(" and search_vector @@ plainto_tsquery('english', $%d)", len(args))
	}

	return query, args
}

func (r *IncidentRepository) List(ctx context.Context, tx pgx.Tx, f ListIncidentsFilter) ([]domain.Incident, error) {
	where, args := incidentWhereClause(f)
	query := `select ` + incidentColumns + ` from incidents` + where

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	query += fmt.Sprintf(" order by opened_at desc limit $%d", len(args))
	args = append(args, f.Offset)
	query += fmt.Sprintf(" offset $%d", len(args))

	incidents, err := queryList(ctx, tx, query, scanIncident, args...)
	if err != nil {
		return nil, err
	}

	ids := make([]uuid.UUID, len(incidents))
	for i, inc := range incidents {
		ids[i] = inc.ID
	}
	assignees, err := r.AssigneesForIncidents(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	for i := range incidents {
		incidents[i].Assignees = orEmptyUserSummarySlice(assignees[incidents[i].ID])
	}
	return incidents, nil
}

// Count returns how many incidents match f, ignoring f.Limit/f.Offset -- used
// alongside List to compute total-page-count for real (non-"load more")
// pagination (see IncidentHandlers.list's X-Total-Count response header).
func (r *IncidentRepository) Count(ctx context.Context, tx pgx.Tx, f ListIncidentsFilter) (int, error) {
	where, args := incidentWhereClause(f)
	query := `select count(*) from incidents` + where

	var count int
	if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count incidents: %w", err)
	}
	return count, nil
}

// orEmptyUserSummarySlice turns a nil slice into an empty one -- a missing
// key in the AssigneesForIncidents map (an incident with no assignees)
// yields nil, which would otherwise serialize as JSON null instead of []
// (see domain.Incident.Assignees's json tag, which has no omitempty).
func orEmptyUserSummarySlice(s []domain.UserSummary) []domain.UserSummary {
	if s == nil {
		return []domain.UserSummary{}
	}
	return s
}

func (r *IncidentRepository) Insert(ctx context.Context, tx pgx.Tx, inc *domain.Incident) error {
	row := tx.QueryRow(ctx, `
		insert into incidents (
			tenant_id, title, description, severity, priority, phase, tags, sla_due_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8)
		returning id, phase, opened_at, created_at, updated_at`,
		inc.TenantID, inc.Title, inc.Description, inc.Severity, inc.Priority, inc.Phase, inc.Tags, inc.SLADueAt,
	)
	return row.Scan(&inc.ID, &inc.Phase, &inc.OpenedAt, &inc.CreatedAt, &inc.UpdatedAt)
}

// SetAssignees replaces an incident's full assignee set (delete-then-bulk-
// insert, same "replace a set" shape as UpdateTags) -- empty userIDs just
// clears every assignee, never an error.
func (r *IncidentRepository) SetAssignees(ctx context.Context, tx pgx.Tx, incidentID, tenantID uuid.UUID, userIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `delete from incident_assignees where incident_id = $1`, incidentID); err != nil {
		return fmt.Errorf("clear assignees: %w", err)
	}
	for _, userID := range userIDs {
		if _, err := tx.Exec(ctx, `
			insert into incident_assignees (incident_id, user_id, tenant_id) values ($1,$2,$3)`,
			incidentID, userID, tenantID,
		); err != nil {
			return fmt.Errorf("insert assignee: %w", err)
		}
	}
	return nil
}

// AssigneesForIncidents batch-loads assignees for every incident in
// incidentIDs in one query (avoiding N+1 selects from Get/List), grouped by
// incident id. An incident with no assignees simply has no key in the
// returned map -- callers should treat a missing key the same as an empty
// slice.
func (r *IncidentRepository) AssigneesForIncidents(ctx context.Context, tx pgx.Tx, incidentIDs []uuid.UUID) (map[uuid.UUID][]domain.UserSummary, error) {
	result := map[uuid.UUID][]domain.UserSummary{}
	if len(incidentIDs) == 0 {
		return result, nil
	}

	rows, err := tx.Query(ctx, `
		select a.incident_id, u.id, u.name
		from incident_assignees a
		join users u on u.id = a.user_id
		where a.incident_id = any($1)
		order by a.incident_id, u.name`,
		incidentIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("query incident assignees: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var incidentID uuid.UUID
		var u domain.UserSummary
		if err := rows.Scan(&incidentID, &u.ID, &u.Name); err != nil {
			return nil, fmt.Errorf("scan incident assignee: %w", err)
		}
		result[incidentID] = append(result[incidentID], u)
	}
	return result, rows.Err()
}

// RolesForIncident loads every NIST-role assignment for one incident,
// ordered by role then assignee name -- matching the display order
// domain.IncidentRoles defines closely enough for a stable, readable list
// (an exact domain.IncidentRoles order would need a `case` expression in
// SQL; plain alphabetical-by-role-string is good enough here since the
// frontend groups by role anyway, not by this ordering).
func (r *IncidentRepository) RolesForIncident(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) ([]domain.IncidentRoleAssignment, error) {
	return queryList(ctx, tx, `
		select ra.role, u.id, u.name
		from incident_role_assignments ra
		join users u on u.id = ra.user_id
		where ra.incident_id = $1
		order by ra.role, u.name`,
		scanIncidentRoleAssignment, incidentID,
	)
}

func scanIncidentRoleAssignment(row pgx.Row) (*domain.IncidentRoleAssignment, error) {
	var ra domain.IncidentRoleAssignment
	if err := row.Scan(&ra.Role, &ra.User.ID, &ra.User.Name); err != nil {
		return nil, fmt.Errorf("scan incident role assignment: %w", err)
	}
	return &ra, nil
}

// SetRole replaces every assignee currently holding role on incident with
// exactly userIDs (delete-then-bulk-insert, same "replace a set" shape as
// SetAssignees/UpdateTags) -- empty userIDs just clears the role. Callers
// must validate cardinality themselves for single-assignee roles (see
// domain.IncidentRole.SingleAssignee and IncidentService.SetRole) --
// the partial unique index in db/migrations/0001_initial_schema.up.sql
// only guards against a concurrent-request race, it's not the primary
// validation path (a bulk-insert of 2 rows for 'commander' would just fail
// with an opaque constraint-violation error otherwise).
func (r *IncidentRepository) SetRole(ctx context.Context, tx pgx.Tx, incidentID, tenantID uuid.UUID, role domain.IncidentRole, userIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `delete from incident_role_assignments where incident_id = $1 and role = $2`, incidentID, role); err != nil {
		return fmt.Errorf("clear role %s: %w", role, err)
	}
	for _, userID := range userIDs {
		if _, err := tx.Exec(ctx, `
			insert into incident_role_assignments (incident_id, user_id, tenant_id, role) values ($1,$2,$3,$4)`,
			incidentID, userID, tenantID, role,
		); err != nil {
			return fmt.Errorf("insert role assignment: %w", err)
		}
	}
	return nil
}

// UpdatePhase moves the incident to phase. It never stamps closed_at itself
// (see MarkClosed for the one place that does) -- only clears it the moment
// the incident moves away from post_incident (reopening), so an incident
// whose phase no longer reads post_incident never keeps reporting a stale
// close time in MTTR/"closed" queries. Reaching post_incident via the phase
// tracker alone does NOT close the incident: closing is IncidentService.Close,
// a distinct explicit action (see its doc comment for why these used to be
// conflated and what broke because of it).
func (r *IncidentRepository) UpdatePhase(ctx context.Context, tx pgx.Tx, id uuid.UUID, phase domain.IncidentPhase) error {
	closedAtExpr := "closed_at"
	if phase != domain.PhasePostIncident {
		closedAtExpr = "null"
	}
	_, err := tx.Exec(ctx, fmt.Sprintf(`
		update incidents
		set phase = $2, closed_at = %s, updated_at = now()
		where id = $1`, closedAtExpr),
		id, phase,
	)
	return err
}

// MarkClosed stamps closed_at (idempotent -- a second call is a no-op) --
// the only place closed_at is ever set. Called exclusively by
// IncidentService.Close, never by a plain phase change.
func (r *IncidentRepository) MarkClosed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		update incidents set closed_at = coalesce(closed_at, now()), updated_at = now() where id = $1`,
		id,
	)
	return err
}

// SetSeverityAndPriority also recomputes sla_due_at for the new
// (severity, priority) pair in the same statement -- a priority change
// should recompute the due date, and clearing it (slaDueAt == nil) when the
// new pair has no configured policy is correct, not a bug: a stale date
// from the old pair would otherwise linger.
func (r *IncidentRepository) SetSeverityAndPriority(ctx context.Context, tx pgx.Tx, id uuid.UUID, severity domain.Severity, priority domain.IncidentPriority, slaDueAt *time.Time) error {
	_, err := tx.Exec(ctx, `
		update incidents set severity = $2, priority = $3, sla_due_at = $4, updated_at = now() where id = $1`,
		id, severity, priority, slaDueAt,
	)
	return err
}

func (r *IncidentRepository) UpdateDescription(ctx context.Context, tx pgx.Tx, id uuid.UUID, description string) error {
	_, err := tx.Exec(ctx, `
		update incidents set description = $2, updated_at = now() where id = $1`,
		id, description,
	)
	return err
}

func (r *IncidentRepository) UpdateTags(ctx context.Context, tx pgx.Tx, id uuid.UUID, tags []string) error {
	_, err := tx.Exec(ctx, `update incidents set tags = $2, updated_at = now() where id = $1`, id, tags)
	return err
}

// RecordPhaseEntered inserts the first-entry row for a phase. A unique
// index on (incident_id, phase) makes this a no-op on revisit, matching
// "not re-added if revisited" from the design handoff. Returns whether a
// row was actually inserted (false = phase already had an entry).
func (r *IncidentRepository) RecordPhaseEntered(ctx context.Context, tx pgx.Tx, incidentID, tenantID uuid.UUID, phase domain.IncidentPhase) (bool, error) {
	tag, err := tx.Exec(ctx, `
		insert into incident_status_history (incident_id, tenant_id, phase)
		values ($1, $2, $3)
		on conflict (incident_id, phase) do nothing`,
		incidentID, tenantID, phase,
	)
	if err != nil {
		return false, fmt.Errorf("record phase entered: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *IncidentRepository) ListStatusHistory(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) ([]domain.IncidentStatusHistoryEntry, error) {
	return queryList(ctx, tx, `
		select id, incident_id, tenant_id, phase, entered_at, corrected_entered_at,
		       corrected_at, corrected_by, correction_reason, created_at
		from incident_status_history
		where incident_id = $1
		order by entered_at asc`,
		scanIncidentStatusHistoryEntry, incidentID,
	)
}

func scanIncidentStatusHistoryEntry(row pgx.Row) (*domain.IncidentStatusHistoryEntry, error) {
	var e domain.IncidentStatusHistoryEntry
	if err := row.Scan(
		&e.ID, &e.IncidentID, &e.TenantID, &e.Phase, &e.EnteredAt, &e.CorrectedEnteredAt,
		&e.CorrectedAt, &e.CorrectedBy, &e.CorrectionReason, &e.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan status history: %w", err)
	}
	return &e, nil
}

// CorrectPhaseTimestamp is the only way to change what a phase's entered_at
// "reads as" after the fact — it never overwrites entered_at itself, so the
// original recorded value is always recoverable. Requires a reason, enforced
// both here and by the DB check constraint.
func (r *IncidentRepository) CorrectPhaseTimestamp(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID, phase domain.IncidentPhase, correctedEnteredAt time.Time, correctedBy uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		update incident_status_history
		set corrected_entered_at = $3, corrected_at = now(), corrected_by = $4, correction_reason = $5
		where incident_id = $1 and phase = $2`,
		incidentID, phase, correctedEnteredAt, correctedBy, reason,
	)
	return err
}

func (r *IncidentRepository) InsertEvent(ctx context.Context, tx pgx.Tx, e *domain.IncidentEvent) error {
	row := tx.QueryRow(ctx, `
		insert into incident_events (incident_id, tenant_id, event_type, actor_type, actor_id, data)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at`,
		e.IncidentID, e.TenantID, e.EventType, e.ActorType, e.ActorID, e.Data,
	)
	return row.Scan(&e.ID, &e.CreatedAt)
}

func (r *IncidentRepository) ListEvents(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) ([]domain.IncidentEvent, error) {
	return queryList(ctx, tx, `
		select id, incident_id, tenant_id, event_type, actor_type, actor_id, data, created_at
		from incident_events
		where incident_id = $1
		order by created_at asc`,
		scanIncidentEvent, incidentID,
	)
}

func scanIncidentEvent(row pgx.Row) (*domain.IncidentEvent, error) {
	var e domain.IncidentEvent
	if err := row.Scan(&e.ID, &e.IncidentID, &e.TenantID, &e.EventType, &e.ActorType, &e.ActorID, &e.Data, &e.CreatedAt); err != nil {
		return nil, fmt.Errorf("scan incident event: %w", err)
	}
	return &e, nil
}

func (r *IncidentRepository) InsertComment(ctx context.Context, tx pgx.Tx, c *domain.IncidentComment) error {
	row := tx.QueryRow(ctx, `
		insert into incident_comments (incident_id, tenant_id, author_id, author_name, body, attachment_url)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at`,
		c.IncidentID, c.TenantID, c.AuthorID, c.AuthorName, c.Body, c.AttachmentURL,
	)
	return row.Scan(&c.ID, &c.CreatedAt)
}

func (r *IncidentRepository) ListComments(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) ([]domain.IncidentComment, error) {
	return queryList(ctx, tx, `
		select id, incident_id, tenant_id, author_id, author_name, body, attachment_url, created_at
		from incident_comments
		where incident_id = $1
		order by created_at asc`,
		scanIncidentComment, incidentID,
	)
}

func scanIncidentComment(row pgx.Row) (*domain.IncidentComment, error) {
	var c domain.IncidentComment
	if err := row.Scan(&c.ID, &c.IncidentID, &c.TenantID, &c.AuthorID, &c.AuthorName, &c.Body, &c.AttachmentURL, &c.CreatedAt); err != nil {
		return nil, fmt.Errorf("scan incident comment: %w", err)
	}
	return &c, nil
}

func (r *IncidentRepository) InsertIOC(ctx context.Context, tx pgx.Tx, ioc *domain.IOC) error {
	row := tx.QueryRow(ctx, `
		insert into incident_iocs (incident_id, tenant_id, type, value, description, identified_at, created_by, created_by_name)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		returning id, created_at`,
		ioc.IncidentID, ioc.TenantID, ioc.Type, ioc.Value, ioc.Description, ioc.IdentifiedAt, ioc.CreatedBy, ioc.CreatedByName,
	)
	return row.Scan(&ioc.ID, &ioc.CreatedAt)
}

// ListIOCs orders newest-identified-first -- the incident detail page's
// IOCs modal (and the postmortem/PDF report sections built from this same
// list) want the most recently identified indicator at the top, not the
// oldest, since that's usually the one still under active investigation.
func (r *IncidentRepository) ListIOCs(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) ([]domain.IOC, error) {
	return queryList(ctx, tx, `
		select id, incident_id, tenant_id, type, value, description, identified_at, created_by, created_by_name, created_at
		from incident_iocs
		where incident_id = $1
		order by identified_at desc`,
		scanIOC, incidentID,
	)
}

func scanIOC(row pgx.Row) (*domain.IOC, error) {
	var i domain.IOC
	if err := row.Scan(&i.ID, &i.IncidentID, &i.TenantID, &i.Type, &i.Value, &i.Description, &i.IdentifiedAt, &i.CreatedBy, &i.CreatedByName, &i.CreatedAt); err != nil {
		return nil, fmt.Errorf("scan ioc: %w", err)
	}
	return &i, nil
}

func (r *IncidentRepository) LinkAlert(ctx context.Context, tx pgx.Tx, incidentID, alertID, tenantID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		insert into incident_alert_links (incident_id, alert_id, tenant_id)
		values ($1,$2,$3)
		on conflict (incident_id, alert_id) do nothing`,
		incidentID, alertID, tenantID,
	)
	return err
}

func (r *IncidentRepository) UnlinkAlert(ctx context.Context, tx pgx.Tx, incidentID, alertID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		delete from incident_alert_links where incident_id = $1 and alert_id = $2`,
		incidentID, alertID,
	)
	return err
}

func (r *IncidentRepository) ListLinkedAlerts(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) ([]domain.Alert, error) {
	return queryList(ctx, tx, `
		select `+alertColumnsWithAssignee+`
		from alerts a
		join incident_alert_links l on l.alert_id = a.id
		left join users u on u.id = a.assigned_analyst_id
		left join playbooks pb on pb.id = a.playbook_id
		where l.incident_id = $1
		order by a.received_at desc`,
		scanAlert, incidentID,
	)
}

func scanIncident(row pgx.Row) (*domain.Incident, error) {
	var inc domain.Incident
	err := row.Scan(
		&inc.ID, &inc.TenantID, &inc.Title, &inc.Description, &inc.Severity, &inc.Priority,
		&inc.Phase, &inc.Tags, &inc.SLADueAt, &inc.SLABreached, &inc.OpenedAt,
		&inc.ClosedAt, &inc.CreatedAt, &inc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan incident: %w", err)
	}
	inc.Assignees = []domain.UserSummary{}
	// Roles are loaded only by Get (RolesForIncident); List deliberately
	// doesn't pay for that second query, since nothing renders team roles
	// in a list. Initialize anyway: left nil, the field serializes as JSON
	// null while the TypeScript type declares IncidentRoleAssignment[] --
	// non-nullable, so nothing warns, and the first component to read
	// incident.roles off a list item dies on .filter of null. See the
	// identical trap this codebase already hit with Assignees above.
	inc.Roles = []domain.IncidentRoleAssignment{}
	return &inc, nil
}
