package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/audit"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
)

// AuditExportService is Settings -> Audit Export: a pull-based CEF export
// of the tenant's full alert/incident event history, for feeding into a
// SIEM (Splunk, ArcSight, QRadar...). See internal/audit for the CEF
// formatting and AuditRepository.ExportEvents for the underlying keyset
// pagination.
type AuditExportService struct {
	pool *db.Pool
	repo *repository.AuditRepository
}

func NewAuditExportService(pool *db.Pool, repo *repository.AuditRepository) *AuditExportService {
	return &AuditExportService{pool: pool, repo: repo}
}

// ExportCursor identifies where to resume a paginated export -- opaque to
// callers beyond round-tripping it through the next request's query params
// (see AuditExportHandlers).
type ExportCursor struct {
	CreatedAt time.Time
	EventID   string
}

// ExportCEF returns up to limit events (oldest first) strictly after
// cursor, formatted as CEF lines, plus the cursor to pass for the next
// page -- nil once there's nothing left to export (a page shorter than
// limit is the signal that this was the last one).
func (s *AuditExportService) ExportCEF(ctx context.Context, tenantID uuid.UUID, cursor *ExportCursor, limit int) ([]string, *ExportCursor, error) {
	events, next, err := s.exportEvents(ctx, tenantID, cursor, limit)
	if err != nil {
		return nil, nil, err
	}
	lines := make([]string, len(events))
	for i, e := range events {
		lines[i] = audit.FormatCEF(e)
	}
	return lines, next, nil
}

// ExportJSON is ExportCEF's counterpart for tooling that wants structured
// data instead of a CEF log line -- same events, same keyset pagination,
// just marshaled as JSON instead of run through audit.FormatCEF.
func (s *AuditExportService) ExportJSON(ctx context.Context, tenantID uuid.UUID, cursor *ExportCursor, limit int) ([]domain.AuditEvent, *ExportCursor, error) {
	return s.exportEvents(ctx, tenantID, cursor, limit)
}

// exportEvents is ExportCEF/ExportJSON's shared cursor resolution + fetch:
// up to limit events (oldest first) strictly after cursor, plus the cursor
// to pass for the next page -- nil once there's nothing left to export (a
// page shorter than limit is the signal that this was the last one).
func (s *AuditExportService) exportEvents(ctx context.Context, tenantID uuid.UUID, cursor *ExportCursor, limit int) ([]domain.AuditEvent, *ExportCursor, error) {
	var events []domain.AuditEvent
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var after *time.Time
		eventID := ""
		if cursor != nil {
			after = &cursor.CreatedAt
			eventID = cursor.EventID
		}
		v, err := s.repo.ExportEvents(ctx, tx, after, eventID, limit)
		events = v
		return err
	})
	if err != nil {
		return nil, nil, fmt.Errorf("export audit events: %w", err)
	}

	var next *ExportCursor
	if len(events) == limit {
		last := events[len(events)-1]
		next = &ExportCursor{CreatedAt: last.CreatedAt, EventID: last.EventID}
	}
	return events, next, nil
}
