package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// AdminAuditLogService is the read side of PR4's Settings-change audit log
// -- backs GET /settings/audit-log (Settings -> Data & Audit). Every write
// side lives in the ~15 feeding services themselves (each calls
// AdminAuditEventRepository.InsertEvent directly inside its own
// pool.WithTenant transaction); this service only reads.
type AdminAuditLogService struct {
	pool  *db.Pool
	repo  *repository.AdminAuditEventRepository
	users *UserService
}

func NewAdminAuditLogService(pool *db.Pool, repo *repository.AdminAuditEventRepository, users *UserService) *AdminAuditLogService {
	return &AdminAuditLogService{pool: pool, repo: repo, users: users}
}

// AdminAuditLogEntry is domain.AdminAuditEvent plus the actor's display
// name, resolved here rather than left to the frontend to look up per row --
// ActorID alone isn't something Settings -> Data & Audit can usefully show
// an admin.
type AdminAuditLogEntry struct {
	domain.AdminAuditEvent
	ActorName string `json:"actorName"`
}

// List returns one page of events, newest first, plus the cursor for the
// next page (nil once there are no more). Same keyset-pagination contract
// as AdminAuditEventRepository.List itself.
//
// The event page and the actor-name lookup are two independent
// pool.WithTenant calls (not one nested inside the other) -- WithTenant
// always begins a fresh transaction from the pool, so nesting would just
// open a second, unrelated connection anyway; two top-level calls says that
// plainly instead of implying a shared transaction that isn't there.
func (s *AdminAuditLogService) List(ctx context.Context, tenantID uuid.UUID, cursor *repository.AdminAuditEventCursor, limit int) ([]AdminAuditLogEntry, *repository.AdminAuditEventCursor, error) {
	var events []domain.AdminAuditEvent
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx, cursor, limit)
		events = v
		return err
	})
	if err != nil {
		return nil, nil, err
	}

	// List, not ListSummaries -- a past audit event's actor may since have
	// been deactivated, and ListSummaries deliberately excludes inactive
	// users (it backs the assignee-picker directory, not a historical
	// record).
	users, err := s.users.List(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	names := make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		names[u.ID] = u.Name
	}

	entries := make([]AdminAuditLogEntry, len(events))
	for i, e := range events {
		entries[i] = AdminAuditLogEntry{AdminAuditEvent: e, ActorName: names[e.ActorID]}
	}

	var next *repository.AdminAuditEventCursor
	if len(events) == limit {
		last := events[len(events)-1]
		next = &repository.AdminAuditEventCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return entries, next, nil
}
