package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

type AuditRepository struct{}

func NewAuditRepository() *AuditRepository {
	return &AuditRepository{}
}

// auditEventUnion is the same four-branch shape RecentActivity uses
// (alert_events/alert_comments/incident_events/incident_comments, each
// joined back to its alert/incident for a title), wrapped in a subquery so
// ExportEvents' keyset WHERE/ORDER BY can reference the union's own output
// columns -- Postgres doesn't allow that directly against a bare UNION ALL.
// event_id is cast to text since the four source tables don't share one ID
// type (bigint identity for the two *_events tables, uuid for the two
// *_comments tables); see domain.AuditEvent's doc comment for why that's
// fine for a tie-breaking cursor column.
//
// Deliberately NOT filtered by allowedTags, unlike RecentActivity -- this
// backs an admin-only compliance/SIEM export (see
// handlers.AuditExportHandlers, gated the same as every other
// /settings/... route), where the whole point is a complete tenant history,
// not a tag-scoped view.
const auditEventUnion = `
	select 'alert' as kind, a.id as context_id, a.title as context_title,
	       ae.event_type, ae.actor_type, ae.actor_id, ae.data, ae.created_at, ae.id::text as event_id
	from alert_events ae join alerts a on a.id = ae.alert_id
	union all
	select 'alert', a.id, a.title, 'comment_added', 'user',
	       c.author_id, jsonb_build_object('authorName', c.author_name)::jsonb, c.created_at, c.id::text
	from alert_comments c join alerts a on a.id = c.alert_id
	union all
	select 'incident', i.id, i.title, ie.event_type, ie.actor_type, ie.actor_id, ie.data, ie.created_at, ie.id::text
	from incident_events ie join incidents i on i.id = ie.incident_id
	union all
	select 'incident', i.id, i.title, 'comment_added', 'user',
	       c.author_id, jsonb_build_object('authorName', c.author_name)::jsonb, c.created_at, c.id::text
	from incident_comments c join incidents i on i.id = c.incident_id`

// ExportEvents returns up to limit events strictly after the (afterCreatedAt,
// afterEventID) cursor, oldest first -- the shape a pull-based SIEM export
// or backfill script pages forward through. A nil afterCreatedAt starts
// from the very beginning of the tenant's history.
func (r *AuditRepository) ExportEvents(ctx context.Context, tx pgx.Tx, afterCreatedAt *time.Time, afterEventID string, limit int) ([]domain.AuditEvent, error) {
	query := `select kind, context_id, context_title, event_type, actor_type, actor_id, data, created_at, event_id
		from (` + auditEventUnion + `) events
		where $1::timestamptz is null or (created_at, event_id) > ($1, $2)
		order by created_at asc, event_id asc
		limit $3`

	return queryList(ctx, tx, query, func(row pgx.Row) (*domain.AuditEvent, error) {
		var e domain.AuditEvent
		if err := row.Scan(&e.Kind, &e.ContextID, &e.ContextTitle, &e.EventType, &e.ActorType, &e.ActorID, &e.Data, &e.CreatedAt, &e.EventID); err != nil {
			return nil, fmt.Errorf("scan audit export event: %w", err)
		}
		return &e, nil
	}, afterCreatedAt, afterEventID, limit)
}
