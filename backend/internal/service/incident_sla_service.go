package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// IncidentSLAService is Settings -> Incident SLAs: lets an admin configure,
// per (severity, priority) pair, how many minutes an incident has before
// it's SLA breached. Lookup is also used directly by IncidentService at
// create/severity-change time to compute sla_due_at.
type IncidentSLAService struct {
	pool *db.Pool
	repo *repository.IncidentSLARepository
}

func NewIncidentSLAService(pool *db.Pool, repo *repository.IncidentSLARepository) *IncidentSLAService {
	return &IncidentSLAService{pool: pool, repo: repo}
}

func (s *IncidentSLAService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.IncidentSLAPolicy, error) {
	var policies []domain.IncidentSLAPolicy
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		policies = v
		return err
	})
	return policies, err
}

// Lookup returns nil (not an error) when the pair has no configured
// policy.
func (s *IncidentSLAService) Lookup(ctx context.Context, tx pgx.Tx, severity domain.Severity, priority domain.IncidentPriority) (*domain.IncidentSLAPolicy, error) {
	return s.repo.Lookup(ctx, tx, severity, priority)
}

// DueAt resolves how much time an incident of this (severity, priority)
// pair has left, computed from now -- nil when unconfigured. Shared by
// IncidentService.Create and SetSeverityAndPriority so both compute
// sla_due_at the exact same way.
func (s *IncidentSLAService) DueAt(ctx context.Context, tx pgx.Tx, severity domain.Severity, priority domain.IncidentPriority) (*time.Time, error) {
	policy, err := s.repo.Lookup(ctx, tx, severity, priority)
	if err != nil {
		return nil, fmt.Errorf("lookup incident sla policy: %w", err)
	}
	if policy == nil {
		return nil, nil
	}
	due := time.Now().Add(time.Duration(policy.DueWithinMinutes) * time.Minute)
	return &due, nil
}

func (s *IncidentSLAService) Save(ctx context.Context, tenantID uuid.UUID, severity domain.Severity, priority domain.IncidentPriority, dueWithinMinutes int) (*domain.IncidentSLAPolicy, error) {
	if dueWithinMinutes <= 0 {
		return nil, fmt.Errorf("dueWithinMinutes must be greater than zero")
	}
	p := &domain.IncidentSLAPolicy{
		TenantID: tenantID, Severity: severity, Priority: priority, DueWithinMinutes: dueWithinMinutes,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Upsert(ctx, tx, p)
	})
	if err != nil {
		return nil, fmt.Errorf("save incident sla policy: %w", err)
	}
	return p, nil
}

func (s *IncidentSLAService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx, id)
	})
}
