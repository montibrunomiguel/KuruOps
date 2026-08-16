package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type EscalationPolicyRepository struct{}

func NewEscalationPolicyRepository() *EscalationPolicyRepository {
	return &EscalationPolicyRepository{}
}

const escalationPolicyColumns = `id, tenant_id, severity, created_at, updated_at`

func (r *EscalationPolicyRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.EscalationPolicy, error) {
	rows, err := tx.Query(ctx, `select `+escalationPolicyColumns+` from escalation_policies order by severity`)
	if err != nil {
		return nil, fmt.Errorf("query escalation policies: %w", err)
	}
	policies := []domain.EscalationPolicy{}
	for rows.Next() {
		p, err := scanEscalationPolicyRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		policies = append(policies, *p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate escalation policies: %w", err)
	}

	// N+1 on step lookup is acceptable here: at most one policy per
	// severity per tenant (five, at most), same reasoning
	// PlaybookRepository.List documents for its own step lookup.
	for i := range policies {
		steps, err := r.stepsFor(ctx, tx, policies[i].ID)
		if err != nil {
			return nil, err
		}
		policies[i].Steps = steps
	}
	return policies, nil
}

// GetBySeverity returns nil (not an error) when no policy is configured for
// severity -- unconfigured is a valid, common state, not a failure.
func (r *EscalationPolicyRepository) GetBySeverity(ctx context.Context, tx pgx.Tx, severity domain.Severity) (*domain.EscalationPolicy, error) {
	row := tx.QueryRow(ctx, `select `+escalationPolicyColumns+` from escalation_policies where severity = $1`, severity)
	p, err := scanEscalationPolicy(row)
	if err != nil || p == nil {
		return nil, err
	}
	steps, err := r.stepsFor(ctx, tx, p.ID)
	if err != nil {
		return nil, err
	}
	p.Steps = steps
	return p, nil
}

// Save creates or replaces the policy for one severity and wholesale-
// replaces its steps -- same delete-then-reinsert idiom
// PlaybookRepository.replaceSteps uses, since the whole chain editor is
// resubmitted on every save.
func (r *EscalationPolicyRepository) Save(ctx context.Context, tx pgx.Tx, p *domain.EscalationPolicy) error {
	row := tx.QueryRow(ctx, `
		insert into escalation_policies (tenant_id, severity)
		values ($1,$2)
		on conflict (tenant_id, severity) do update set updated_at = now()
		returning id, created_at, updated_at`,
		p.TenantID, p.Severity,
	)
	if err := row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return fmt.Errorf("upsert escalation policy: %w", err)
	}
	return r.replaceSteps(ctx, tx, p.ID, p.TenantID, p.Steps)
}

func (r *EscalationPolicyRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from escalation_policies where id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete escalation policy: %w", err)
	}
	return nil
}

// replaceSteps deletes and reinserts every step for the policy -- step ids
// are always DB-generated fresh here (the incoming steps' own IDs, if any,
// are ignored), same convention as PlaybookRepository.replaceSteps.
func (r *EscalationPolicyRepository) replaceSteps(ctx context.Context, tx pgx.Tx, policyID, tenantID uuid.UUID, steps []domain.EscalationStep) error {
	if _, err := tx.Exec(ctx, `delete from escalation_policy_steps where policy_id = $1`, policyID); err != nil {
		return fmt.Errorf("clear escalation policy steps: %w", err)
	}
	for i, step := range steps {
		if _, err := tx.Exec(ctx, `
			insert into escalation_policy_steps
				(policy_id, tenant_id, position, schedule_id, delay_minutes, channel_type, destination_secret_ref, webhook_payload_template)
			values ($1,$2,$3,$4,$5,$6,$7,$8)`,
			policyID, tenantID, i, step.ScheduleID, step.DelayMinutes, step.ChannelType, step.DestinationSecretRef, step.WebhookPayloadTemplate,
		); err != nil {
			return fmt.Errorf("insert escalation policy step: %w", err)
		}
	}
	return nil
}

func (r *EscalationPolicyRepository) stepsFor(ctx context.Context, tx pgx.Tx, policyID uuid.UUID) ([]domain.EscalationStep, error) {
	rows, err := tx.Query(ctx, `
		select s.id, s.position, s.schedule_id, o.name, s.delay_minutes, s.channel_type, s.destination_secret_ref, s.webhook_payload_template
		from escalation_policy_steps s
		join on_call_schedules o on o.id = s.schedule_id
		where s.policy_id = $1
		order by s.position asc`,
		policyID,
	)
	if err != nil {
		return nil, fmt.Errorf("query escalation policy steps: %w", err)
	}
	defer rows.Close()

	steps := []domain.EscalationStep{}
	for rows.Next() {
		var s domain.EscalationStep
		if err := rows.Scan(&s.ID, &s.Position, &s.ScheduleID, &s.ScheduleName, &s.DelayMinutes, &s.ChannelType, &s.DestinationSecretRef, &s.WebhookPayloadTemplate); err != nil {
			return nil, fmt.Errorf("scan escalation policy step: %w", err)
		}
		s.PolicyID = policyID
		steps = append(steps, s)
	}
	return steps, rows.Err()
}

func scanEscalationPolicy(row pgx.Row) (*domain.EscalationPolicy, error) {
	var p domain.EscalationPolicy
	err := row.Scan(&p.ID, &p.TenantID, &p.Severity, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan escalation policy: %w", err)
	}
	return &p, nil
}

// scanEscalationPolicyRow is scanEscalationPolicy's pgx.Rows counterpart
// (List iterates rows, not a single QueryRow) -- same column order, no
// ErrNoRows case since Rows.Next() already gates that.
func scanEscalationPolicyRow(rows pgx.Rows) (*domain.EscalationPolicy, error) {
	var p domain.EscalationPolicy
	if err := rows.Scan(&p.ID, &p.TenantID, &p.Severity, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("scan escalation policy: %w", err)
	}
	return &p, nil
}
