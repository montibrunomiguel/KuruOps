package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

// AlertRepository is the only place that writes SQL for the alerts table.
// It intentionally takes a pgx.Tx (not the pool) for every method, because
// every call must happen inside db.Pool.WithTenant — that's what sets
// app.tenant_id and lets row-level security do its job. A method here that
// accepted a bare pool would make it possible to query without a tenant
// scope, which is exactly the bug class this repository exists to prevent.
type AlertRepository struct{}

func NewAlertRepository() *AlertRepository {
	return &AlertRepository{}
}

// alertColumnsQualified qualifies every alerts column to the "a" alias --
// needed wherever alerts is joined against another table that also has a
// tenant_id column (e.g. incident_alert_links, users), since an unqualified
// "tenant_id" in the select list is ambiguous the moment two tables in the
// FROM clause share that column name, regardless of which one was intended.
const alertColumnsQualified = `
	a.id, a.tenant_id, a.external_id, a.webhook_endpoint_id, a.title, a.source,
	a.severity, a.original_severity, a.status, a.classification, a.close_comment,
	a.close_image_url, a.rule_id, a.asset, a.src_ip, a.tags, a.payload, a.metadata, a.incident_id,
	a.assigned_analyst_id, a.received_at, a.acknowledged_at, a.closed_at, a.created_at, a.updated_at`

// alertColumnsWithAssignee/alertsWithAssigneeFrom resolve
// domain.Alert.AssignedAnalystName via a live join, same reasoning and shape
// as incidentColumnsWithOwner/incidentsWithOwnerFrom in incident_repository.go.
const alertColumnsWithAssignee = alertColumnsQualified + `,
	u.name`

const alertsWithAssigneeFrom = `from alerts a left join users u on u.id = a.assigned_analyst_id`

func (r *AlertRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Alert, error) {
	row := tx.QueryRow(ctx, `select `+alertColumnsWithAssignee+` `+alertsWithAssigneeFrom+` where a.id = $1`, id)
	return scanAlert(row)
}

type ListAlertsFilter struct {
	Severity *domain.Severity
	Status   *domain.AlertStatus
	// Statuses is an OR'd alternative to Status, for callers that need "any
	// of these statuses" (e.g. the Follow-up view: escalated OR
	// investigating) rather than a single exact match. Set at most one of
	// Status/Statuses -- if both are set, Status wins (Statuses is ignored).
	Statuses   []domain.AlertStatus
	Source     *string
	Tag        *string
	Correlated *bool
	// ReceivedSince/ReceivedUntil restrict to alerts received within
	// [ReceivedSince, ReceivedUntil] -- the Dashboard's time-range filter
	// (see repository.StatsFilter.Since/Until), either end optional.
	ReceivedSince *time.Time
	ReceivedUntil *time.Time
	// AllowedTags scopes results to the caller's tag-based access (see
	// design handoff, "Tag-based + resource-based access scoping"). Empty
	// means unrestricted -- no filter applied, same "sees everything"
	// semantics as domain.User.AllowedTags. Set from
	// middleware.AllowedTags(ctx) by the handler, not user-suppliable.
	AllowedTags []string
	Limit       int
	Offset      int
}

func (r *AlertRepository) List(ctx context.Context, tx pgx.Tx, f ListAlertsFilter) ([]domain.Alert, error) {
	query := `select ` + alertColumnsWithAssignee + ` ` + alertsWithAssigneeFrom + ` where 1 = 1`
	args := []any{}

	if f.Severity != nil {
		args = append(args, *f.Severity)
		query += fmt.Sprintf(" and a.severity = $%d", len(args))
	}
	if f.Status != nil {
		args = append(args, *f.Status)
		query += fmt.Sprintf(" and a.status = $%d", len(args))
	} else if len(f.Statuses) > 0 {
		// pgx has no registered codec for []domain.AlertStatus (a named
		// string slice) against the alert_status_enum[] parameter type it
		// would need to infer -- "cannot find encode plan" at query time.
		// Passing []string with an explicit cast sidesteps that: pgx knows
		// how to encode []string, and Postgres casts each element back to
		// the enum for the comparison.
		statuses := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			statuses[i] = string(s)
		}
		args = append(args, statuses)
		query += fmt.Sprintf(" and a.status = any($%d::alert_status_enum[])", len(args))
	}
	if f.Source != nil {
		args = append(args, *f.Source)
		query += fmt.Sprintf(" and a.source = $%d", len(args))
	}
	if f.Tag != nil {
		args = append(args, *f.Tag)
		query += fmt.Sprintf(" and $%d = any(a.tags)", len(args))
	}
	if f.Correlated != nil {
		if *f.Correlated {
			query += " and a.incident_id is not null"
		} else {
			query += " and a.incident_id is null"
		}
	}
	if f.ReceivedSince != nil {
		args = append(args, *f.ReceivedSince)
		query += fmt.Sprintf(" and a.received_at >= $%d", len(args))
	}
	if f.ReceivedUntil != nil {
		args = append(args, *f.ReceivedUntil)
		query += fmt.Sprintf(" and a.received_at <= $%d", len(args))
	}
	if len(f.AllowedTags) > 0 {
		args = append(args, f.AllowedTags)
		query += fmt.Sprintf(" and a.tags && $%d", len(args))
	}

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	query += fmt.Sprintf(" order by a.received_at desc limit $%d", len(args))
	args = append(args, f.Offset)
	query += fmt.Sprintf(" offset $%d", len(args))

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query alerts: %w", err)
	}
	defer rows.Close()

	alerts := []domain.Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, *a)
	}
	return alerts, rows.Err()
}

func (r *AlertRepository) Insert(ctx context.Context, tx pgx.Tx, a *domain.Alert) error {
	// alerts.metadata is NOT NULL -- defaulted here (not just in
	// AlertService.Ingest) so every direct-repository caller (every test
	// fixture built before this column existed) keeps working without
	// having to set Metadata itself, same as the column's own `default
	// '{}'::jsonb` would give a bare INSERT that omitted it entirely.
	if len(a.Metadata) == 0 {
		a.Metadata = json.RawMessage(`{}`)
	}
	row := tx.QueryRow(ctx, `
		insert into alerts (
			tenant_id, external_id, webhook_endpoint_id, title, source,
			severity, original_severity, status, tags, payload, metadata, rule_id, asset, src_ip,
			assigned_analyst_id, received_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		returning id, created_at, updated_at,
			(select name from users where id = assigned_analyst_id)`,
		a.TenantID, a.ExternalID, a.WebhookEndpointID, a.Title, a.Source,
		a.Severity, a.OriginalSeverity, a.Status, a.Tags, a.Payload, a.Metadata, a.RuleID, a.Asset, a.SrcIP,
		a.AssignedAnalystID, a.ReceivedAt,
	)
	return row.Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt, &a.AssignedAnalystName)
}

// UpdateStatus moves the alert to a new status. Passing stampAcknowledged=true
// sets acknowledged_at (only if it is still null) — used the first time an
// alert leaves 'open', matching the prototype's MTTA definition.
func (r *AlertRepository) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.AlertStatus, stampAcknowledged bool) error {
	_, err := tx.Exec(ctx, `
		update alerts
		set status = $2,
		    acknowledged_at = case when $3 then coalesce(acknowledged_at, now()) else acknowledged_at end,
		    updated_at = now()
		where id = $1`,
		id, status, stampAcknowledged,
	)
	return err
}

func (r *AlertRepository) UpdateTags(ctx context.Context, tx pgx.Tx, id uuid.UUID, tags []string) error {
	_, err := tx.Exec(ctx, `update alerts set tags = $2, updated_at = now() where id = $1`, id, tags)
	return err
}

func (r *AlertRepository) Close(ctx context.Context, tx pgx.Tx, id uuid.UUID, in domain.CloseAlertInput) error {
	_, err := tx.Exec(ctx, `
		update alerts
		set status = 'closed',
		    classification = $2,
		    close_comment = $3,
		    close_image_url = $4,
		    closed_at = now(),
		    updated_at = now()
		where id = $1`,
		id, in.Classification, in.Comment, in.ImageURL,
	)
	return err
}

// UpdateSeverity is used by the manual "Override Severity" action. It never
// touches original_severity — that column keeps recording what the alert
// arrived as (see domain.Alert.OriginalSeverity), so the override is always
// visible as a delta rather than overwriting history.
func (r *AlertRepository) UpdateSeverity(ctx context.Context, tx pgx.Tx, id uuid.UUID, severity domain.Severity) error {
	_, err := tx.Exec(ctx, `update alerts set severity = $2, updated_at = now() where id = $1`, id, severity)
	return err
}

// UpdateAssignee sets or clears the alert's single assigned analyst --
// analystID nil clears it (unassigns).
func (r *AlertRepository) UpdateAssignee(ctx context.Context, tx pgx.Tx, id uuid.UUID, analystID *uuid.UUID) error {
	_, err := tx.Exec(ctx, `update alerts set assigned_analyst_id = $2, updated_at = now() where id = $1`, id, analystID)
	return err
}

// LinkAlert inserts both (a,b) and (b,a) rows so alert_links reads
// symmetrically from either side -- ListLinkedAlerts never needs an OR'd
// alert_id/linked_alert_id lookup. The table itself has no symmetry
// constraint, so this pair-insert is what makes it behave symmetrically.
func (r *AlertRepository) LinkAlert(ctx context.Context, tx pgx.Tx, alertID, otherID, tenantID, createdBy uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		insert into alert_links (alert_id, linked_alert_id, tenant_id, created_by)
		values ($1,$2,$3,$4), ($2,$1,$3,$4)
		on conflict (alert_id, linked_alert_id) do nothing`,
		alertID, otherID, tenantID, createdBy,
	)
	return err
}

// UnlinkAlert removes both directions of the pair inserted by LinkAlert.
func (r *AlertRepository) UnlinkAlert(ctx context.Context, tx pgx.Tx, alertID, otherID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		delete from alert_links
		where (alert_id = $1 and linked_alert_id = $2) or (alert_id = $2 and linked_alert_id = $1)`,
		alertID, otherID,
	)
	return err
}

func (r *AlertRepository) ListLinkedAlerts(ctx context.Context, tx pgx.Tx, alertID uuid.UUID) ([]domain.Alert, error) {
	rows, err := tx.Query(ctx, `
		select `+alertColumnsWithAssignee+`
		from alerts a
		join alert_links l on l.linked_alert_id = a.id
		left join users u on u.id = a.assigned_analyst_id
		where l.alert_id = $1
		order by a.received_at desc`,
		alertID,
	)
	if err != nil {
		return nil, fmt.Errorf("query linked alerts: %w", err)
	}
	defer rows.Close()

	alerts := []domain.Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, *a)
	}
	return alerts, rows.Err()
}

// InsertComment/ListComments back Team Notes on an alert -- same shape as
// IncidentRepository.InsertComment/ListComments.
func (r *AlertRepository) InsertComment(ctx context.Context, tx pgx.Tx, c *domain.AlertComment) error {
	row := tx.QueryRow(ctx, `
		insert into alert_comments (alert_id, tenant_id, author_id, author_name, body, image_url)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at`,
		c.AlertID, c.TenantID, c.AuthorID, c.AuthorName, c.Body, c.ImageURL,
	)
	return row.Scan(&c.ID, &c.CreatedAt)
}

func (r *AlertRepository) ListComments(ctx context.Context, tx pgx.Tx, alertID uuid.UUID) ([]domain.AlertComment, error) {
	rows, err := tx.Query(ctx, `
		select id, alert_id, tenant_id, author_id, author_name, body, image_url, created_at
		from alert_comments
		where alert_id = $1
		order by created_at asc`,
		alertID,
	)
	if err != nil {
		return nil, fmt.Errorf("query alert comments: %w", err)
	}
	defer rows.Close()

	comments := []domain.AlertComment{}
	for rows.Next() {
		var c domain.AlertComment
		if err := rows.Scan(&c.ID, &c.AlertID, &c.TenantID, &c.AuthorID, &c.AuthorName, &c.Body, &c.ImageURL, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan alert comment: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

func (r *AlertRepository) InsertEvent(ctx context.Context, tx pgx.Tx, e *domain.AlertEvent) error {
	row := tx.QueryRow(ctx, `
		insert into alert_events (alert_id, tenant_id, event_type, actor_type, actor_id, data)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at`,
		e.AlertID, e.TenantID, e.EventType, e.ActorType, e.ActorID, e.Data,
	)
	return row.Scan(&e.ID, &e.CreatedAt)
}

func scanAlert(row pgx.Row) (*domain.Alert, error) {
	var a domain.Alert
	err := row.Scan(
		&a.ID, &a.TenantID, &a.ExternalID, &a.WebhookEndpointID, &a.Title, &a.Source,
		&a.Severity, &a.OriginalSeverity, &a.Status, &a.Classification, &a.CloseComment,
		&a.CloseImageURL, &a.RuleID, &a.Asset, &a.SrcIP, &a.Tags, &a.Payload, &a.Metadata, &a.IncidentID,
		&a.AssignedAnalystID, &a.ReceivedAt, &a.AcknowledgedAt, &a.ClosedAt, &a.CreatedAt, &a.UpdatedAt,
		&a.AssignedAnalystName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan alert: %w", err)
	}
	return &a, nil
}
