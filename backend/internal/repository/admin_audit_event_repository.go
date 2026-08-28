package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

// AdminAuditEventRepository is admin_audit_events' data access -- see
// domain.AdminAuditEvent for the row shape and why it's a separate table
// from alert_events/incident_events.
type AdminAuditEventRepository struct{}

func NewAdminAuditEventRepository() *AdminAuditEventRepository {
	return &AdminAuditEventRepository{}
}

// InsertEvent mirrors AlertRepository.InsertEvent/IncidentRepository.InsertEvent
// -- always called inside the same transaction as the state-changing write
// it records, so a failed audit insert rolls back the state change too.
func (r *AdminAuditEventRepository) InsertEvent(ctx context.Context, tx pgx.Tx, e *domain.AdminAuditEvent) error {
	row := tx.QueryRow(ctx, `
		insert into admin_audit_events (tenant_id, area, action, actor_type, actor_id, data)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at`,
		e.TenantID, e.Area, e.Action, e.ActorType, e.ActorID, e.Data,
	)
	return row.Scan(&e.ID, &e.CreatedAt)
}

// AdminAuditEventCursor identifies where to resume a paginated List --
// opaque to callers beyond round-tripping it through the next request's
// query params, same shape as AuditExportService's ExportCursor.
type AdminAuditEventCursor struct {
	CreatedAt time.Time
	ID        int64
}

// List returns up to limit events newest-first, strictly before cursor (nil
// for the first page) -- a browsing UI for "what changed recently" wants
// newest-first, unlike AuditRepository.ExportEvents' oldest-first CEF/SIEM
// export, which needs a stable resume point for a one-time full pull.
func (r *AdminAuditEventRepository) List(ctx context.Context, tx pgx.Tx, cursor *AdminAuditEventCursor, limit int) ([]domain.AdminAuditEvent, error) {
	var beforeCreatedAt *time.Time
	var beforeID int64
	if cursor != nil {
		beforeCreatedAt = &cursor.CreatedAt
		beforeID = cursor.ID
	}

	query := `
		select id, tenant_id, area, action, actor_type, actor_id, data, created_at
		from admin_audit_events
		where $1::timestamptz is null or (created_at, id) < ($1, $2)
		order by created_at desc, id desc
		limit $3`

	return queryList(ctx, tx, query, func(row pgx.Row) (*domain.AdminAuditEvent, error) {
		var e domain.AdminAuditEvent
		if err := row.Scan(&e.ID, &e.TenantID, &e.Area, &e.Action, &e.ActorType, &e.ActorID, &e.Data, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan admin audit event: %w", err)
		}
		return &e, nil
	}, beforeCreatedAt, beforeID, limit)
}
