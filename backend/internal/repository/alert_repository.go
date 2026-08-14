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
//
// The incident_id slot is NOT the raw a.incident_id column -- that column is
// never written by any code path (escalating an alert, or linking one to an
// incident from its "Correlated Alerts" panel, both only ever insert into
// incident_alert_links; nothing does `update alerts set incident_id = ...`).
// Keeping a stored column in sync across every place a link can be created
// or removed is exactly the kind of denormalization bug this ended up
// being -- so instead this computes it at read time from the link table
// itself: the most recently linked incident, if any. scanAlert (below)
// scans by position, not by column name, so this slots into
// domain.Alert.IncidentID exactly like a real column would.
const alertColumnsQualified = `
	a.id, a.tenant_id, a.external_id, a.webhook_endpoint_id, a.title, a.source,
	a.severity, a.original_severity, a.status, a.classification, a.close_comment,
	a.close_attachment_url, a.rule_id, a.asset, a.src_ip, a.tags, a.payload, a.metadata, a.duplicate_count,
	(select l.incident_id from incident_alert_links l where l.alert_id = a.id order by l.linked_at desc limit 1),
	a.playbook_id,
	a.assigned_analyst_id, a.received_at, a.acknowledged_at, a.closed_at, a.created_at, a.updated_at`

// alertColumnsWithAssignee/alertsWithAssigneeFrom resolve
// domain.Alert.AssignedAnalystName/PlaybookTitle via live joins, same
// reasoning and shape as incidentColumnsWithOwner/incidentsWithOwnerFrom in
// incident_repository.go.
const alertColumnsWithAssignee = alertColumnsQualified + `,
	u.name, pb.title`

const alertsWithAssigneeFrom = `from alerts a left join users u on u.id = a.assigned_analyst_id left join playbooks pb on pb.id = a.playbook_id`

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

// alertWhereClause builds the "where ..." fragment (starting with "where 1 =
// 1" so every branch below can unconditionally prepend "and") plus its
// positional args, shared by List and Count so the two can never drift apart
// on which rows they consider a match.
func alertWhereClause(f ListAlertsFilter) (string, []any) {
	query := " where 1 = 1"
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
			query += " and exists (select 1 from incident_alert_links l where l.alert_id = a.id)"
		} else {
			query += " and not exists (select 1 from incident_alert_links l where l.alert_id = a.id)"
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

	return query, args
}

func (r *AlertRepository) List(ctx context.Context, tx pgx.Tx, f ListAlertsFilter) ([]domain.Alert, error) {
	where, args := alertWhereClause(f)
	query := `select ` + alertColumnsWithAssignee + ` ` + alertsWithAssigneeFrom + where

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

// Count returns how many alerts match f, ignoring f.Limit/f.Offset -- used
// alongside List to compute total-page-count for real (non-"load more")
// pagination (see AlertHandlers.list's X-Total-Count response header).
func (r *AlertRepository) Count(ctx context.Context, tx pgx.Tx, f ListAlertsFilter) (int, error) {
	where, args := alertWhereClause(f)
	query := `select count(*) from alerts a` + where

	var count int
	if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count alerts: %w", err)
	}
	return count, nil
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
			assigned_analyst_id, received_at, group_key, playbook_id
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		returning id, created_at, updated_at,
			(select name from users where id = assigned_analyst_id),
			(select title from playbooks where id = playbook_id)`,
		a.TenantID, a.ExternalID, a.WebhookEndpointID, a.Title, a.Source,
		a.Severity, a.OriginalSeverity, a.Status, a.Tags, a.Payload, a.Metadata, a.RuleID, a.Asset, a.SrcIP,
		a.AssignedAnalystID, a.ReceivedAt, a.GroupKey, a.PlaybookID,
	)
	return row.Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt, &a.AssignedAnalystName, &a.PlaybookTitle)
}

// FindAndIncrementDuplicate is the whole dedup match-and-suppress step in
// one atomic statement: it looks for the most recently received alert on
// webhookEndpointID whose group_key equals groupKey and whose received_at
// is still within windowMinutes, and if found, increments its
// duplicate_count and bumps updated_at in the same UPDATE. found=false
// (with a zero id/newCount) means no match -- the caller should insert a
// fresh alert instead, exactly like it would with dedup off.
//
// This alone does not fully serialize two concurrent ingests for the same
// (webhookEndpointID, groupKey) that both arrive before either has been
// inserted yet -- both would find nothing and both would insert. See
// AlertService.Ingest's doc comment for the pg_advisory_xact_lock that
// closes that window; this method assumes the caller already holds it.
func (r *AlertRepository) FindAndIncrementDuplicate(ctx context.Context, tx pgx.Tx, webhookEndpointID uuid.UUID, groupKey string, windowMinutes int) (id uuid.UUID, newCount int, found bool, err error) {
	err = tx.QueryRow(ctx, `
		update alerts
		set duplicate_count = duplicate_count + 1, updated_at = now()
		where id = (
			select id from alerts
			where webhook_endpoint_id = $1 and group_key = $2
			  and received_at > now() - make_interval(mins => $3)
			order by received_at desc
			limit 1
		)
		returning id, duplicate_count`,
		// make_interval(mins => $3), not ($3 || ' minutes')::interval -- the
		// escalation_policies precedent for that string-concat idiom always
		// binds a column reference there (already typed integer by the
		// table), not a bare Go int parameter. pgx can't infer a type for
		// $3 against the || operator's text operand ("cannot find encode
		// plan"), the same class of bug as the severity/status enum-array
		// casts fixed earlier -- make_interval's own signature gives $3 an
		// unambiguous integer type instead.
		webhookEndpointID, groupKey, windowMinutes,
	).Scan(&id, &newCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, 0, false, nil
		}
		return uuid.Nil, 0, false, fmt.Errorf("find and increment duplicate: %w", err)
	}
	return id, newCount, true, nil
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
		    close_attachment_url = $4,
		    closed_at = now(),
		    updated_at = now()
		where id = $1`,
		id, in.Classification, in.Comment, in.AttachmentURL,
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
		left join playbooks pb on pb.id = a.playbook_id
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
		insert into alert_comments (alert_id, tenant_id, author_id, author_name, body, attachment_url)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at`,
		c.AlertID, c.TenantID, c.AuthorID, c.AuthorName, c.Body, c.AttachmentURL,
	)
	return row.Scan(&c.ID, &c.CreatedAt)
}

func (r *AlertRepository) ListComments(ctx context.Context, tx pgx.Tx, alertID uuid.UUID) ([]domain.AlertComment, error) {
	rows, err := tx.Query(ctx, `
		select id, alert_id, tenant_id, author_id, author_name, body, attachment_url, created_at
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
		if err := rows.Scan(&c.ID, &c.AlertID, &c.TenantID, &c.AuthorID, &c.AuthorName, &c.Body, &c.AttachmentURL, &c.CreatedAt); err != nil {
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
		&a.CloseAttachmentURL, &a.RuleID, &a.Asset, &a.SrcIP, &a.Tags, &a.Payload, &a.Metadata, &a.DuplicateCount, &a.IncidentID,
		&a.PlaybookID,
		&a.AssignedAnalystID, &a.ReceivedAt, &a.AcknowledgedAt, &a.ClosedAt, &a.CreatedAt, &a.UpdatedAt,
		&a.AssignedAnalystName, &a.PlaybookTitle,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan alert: %w", err)
	}
	return &a, nil
}
