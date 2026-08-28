package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
)

// IncidentSLAService is Settings -> Incident SLAs: lets an admin configure,
// per (severity, priority) pair, how many minutes an incident has before
// it's SLA breached. DueAt is used directly by IncidentService at
// create/severity-change time to compute sla_due_at.
type IncidentSLAService struct {
	pool  *db.Pool
	repo  *repository.IncidentSLARepository
	audit *repository.AdminAuditEventRepository
}

func NewIncidentSLAService(pool *db.Pool, repo *repository.IncidentSLARepository, audit *repository.AdminAuditEventRepository) *IncidentSLAService {
	return &IncidentSLAService{pool: pool, repo: repo, audit: audit}
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

func (s *IncidentSLAService) Save(ctx context.Context, tenantID, actorID uuid.UUID, severity domain.Severity, priority domain.IncidentPriority, dueWithinMinutes int) (*domain.IncidentSLAPolicy, error) {
	if dueWithinMinutes <= 0 {
		return nil, fmt.Errorf("dueWithinMinutes must be greater than zero")
	}
	p := &domain.IncidentSLAPolicy{
		TenantID: tenantID, Severity: severity, Priority: priority, DueWithinMinutes: dueWithinMinutes,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		before, err := s.repo.Lookup(ctx, tx, severity, priority)
		if err != nil {
			return err
		}
		if err := s.repo.Upsert(ctx, tx, p); err != nil {
			return err
		}
		var fromMinutes any
		if before != nil {
			fromMinutes = before.DueWithinMinutes
		}
		data, _ := json.Marshal(map[string]any{
			"from": map[string]any{"severity": severity, "priority": priority, "dueWithinMinutes": fromMinutes},
			"to":   map[string]any{"severity": severity, "priority": priority, "dueWithinMinutes": dueWithinMinutes},
		})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "incident-sla", Action: "save", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("save incident sla policy: %w", err)
	}
	return p, nil
}

func (s *IncidentSLAService) Delete(ctx context.Context, tenantID, actorID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.Delete(ctx, tx, id); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": map[string]any{"id": id}, "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "incident-sla", Action: "delete", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}
