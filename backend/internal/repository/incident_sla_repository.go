package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type IncidentSLARepository struct{}

func NewIncidentSLARepository() *IncidentSLARepository {
	return &IncidentSLARepository{}
}

const incidentSLAPolicyColumns = `id, tenant_id, severity, priority, due_within_minutes, created_at, updated_at`

func (r *IncidentSLARepository) List(ctx context.Context, tx pgx.Tx) ([]domain.IncidentSLAPolicy, error) {
	rows, err := tx.Query(ctx, `select `+incidentSLAPolicyColumns+` from incident_sla_policies order by severity, priority`)
	if err != nil {
		return nil, fmt.Errorf("query incident sla policies: %w", err)
	}
	defer rows.Close()

	policies := []domain.IncidentSLAPolicy{}
	for rows.Next() {
		p, err := scanIncidentSLAPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, *p)
	}
	return policies, rows.Err()
}

// Lookup returns nil (not an error) when no policy is configured for the
// pair -- unconfigured is a valid, common state, not a failure.
func (r *IncidentSLARepository) Lookup(ctx context.Context, tx pgx.Tx, severity domain.Severity, priority domain.IncidentPriority) (*domain.IncidentSLAPolicy, error) {
	row := tx.QueryRow(ctx, `
		select `+incidentSLAPolicyColumns+` from incident_sla_policies
		where severity = $1 and priority = $2`,
		severity, priority,
	)
	p, err := scanIncidentSLAPolicy(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

// Upsert creates or replaces the policy for one (severity, priority) pair.
func (r *IncidentSLARepository) Upsert(ctx context.Context, tx pgx.Tx, p *domain.IncidentSLAPolicy) error {
	row := tx.QueryRow(ctx, `
		insert into incident_sla_policies (tenant_id, severity, priority, due_within_minutes)
		values ($1,$2,$3,$4)
		on conflict (tenant_id, severity, priority) do update set
			due_within_minutes = excluded.due_within_minutes, updated_at = now()
		returning id, created_at, updated_at`,
		p.TenantID, p.Severity, p.Priority, p.DueWithinMinutes,
	)
	if err := row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return fmt.Errorf("upsert incident sla policy: %w", err)
	}
	return nil
}

func (r *IncidentSLARepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from incident_sla_policies where id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete incident sla policy: %w", err)
	}
	return nil
}

func scanIncidentSLAPolicy(row pgx.Row) (*domain.IncidentSLAPolicy, error) {
	var p domain.IncidentSLAPolicy
	err := row.Scan(&p.ID, &p.TenantID, &p.Severity, &p.Priority, &p.DueWithinMinutes, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
