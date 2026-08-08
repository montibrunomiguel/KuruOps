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

const escalationPolicyColumns = `id, tenant_id, severity, unacknowledged_after_minutes, channel_type, destination_secret_ref, webhook_payload_template, created_at, updated_at`

func (r *EscalationPolicyRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.EscalationPolicy, error) {
	rows, err := tx.Query(ctx, `select `+escalationPolicyColumns+` from escalation_policies order by severity`)
	if err != nil {
		return nil, fmt.Errorf("query escalation policies: %w", err)
	}
	defer rows.Close()

	policies := []domain.EscalationPolicy{}
	for rows.Next() {
		p, err := scanEscalationPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, *p)
	}
	return policies, rows.Err()
}

// GetBySeverity returns nil (not an error) when no policy is configured for
// severity -- unconfigured is a valid, common state, not a failure.
func (r *EscalationPolicyRepository) GetBySeverity(ctx context.Context, tx pgx.Tx, severity domain.Severity) (*domain.EscalationPolicy, error) {
	row := tx.QueryRow(ctx, `select `+escalationPolicyColumns+` from escalation_policies where severity = $1`, severity)
	p, err := scanEscalationPolicy(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

// Upsert creates or replaces the policy for one severity.
func (r *EscalationPolicyRepository) Upsert(ctx context.Context, tx pgx.Tx, p *domain.EscalationPolicy) error {
	row := tx.QueryRow(ctx, `
		insert into escalation_policies (tenant_id, severity, unacknowledged_after_minutes, channel_type, destination_secret_ref, webhook_payload_template)
		values ($1,$2,$3,$4,$5,$6)
		on conflict (tenant_id, severity) do update set
			unacknowledged_after_minutes = excluded.unacknowledged_after_minutes,
			channel_type = excluded.channel_type,
			destination_secret_ref = excluded.destination_secret_ref,
			webhook_payload_template = excluded.webhook_payload_template,
			updated_at = now()
		returning id, created_at, updated_at`,
		p.TenantID, p.Severity, p.UnacknowledgedAfterMinutes, p.ChannelType, p.DestinationSecretRef, p.WebhookPayloadTemplate,
	)
	if err := row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return fmt.Errorf("upsert escalation policy: %w", err)
	}
	return nil
}

func (r *EscalationPolicyRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from escalation_policies where id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete escalation policy: %w", err)
	}
	return nil
}

func scanEscalationPolicy(row pgx.Row) (*domain.EscalationPolicy, error) {
	var p domain.EscalationPolicy
	err := row.Scan(&p.ID, &p.TenantID, &p.Severity, &p.UnacknowledgedAfterMinutes, &p.ChannelType, &p.DestinationSecretRef, &p.WebhookPayloadTemplate, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
