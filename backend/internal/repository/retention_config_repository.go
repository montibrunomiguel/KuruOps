package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

// RetentionConfigRepository is the single-row-per-tenant "how long do
// closed alerts/incidents stick around" config -- same shape as
// SMTPConfigRepository. Get returning nil means no row has ever been
// saved -- RetentionConfigService.Get is what turns that into the
// domain.DefaultRetentionMonths default; this repository just reports what
// is (or isn't) actually stored.
type RetentionConfigRepository struct{}

func NewRetentionConfigRepository() *RetentionConfigRepository {
	return &RetentionConfigRepository{}
}

func (r *RetentionConfigRepository) Get(ctx context.Context, tx pgx.Tx) (*domain.RetentionConfig, error) {
	var c domain.RetentionConfig
	err := tx.QueryRow(ctx, `
		select tenant_id, alert_retention_months, incident_retention_months, updated_at
		from tenant_retention_config limit 1`,
	).Scan(&c.TenantID, &c.AlertRetentionMonths, &c.IncidentRetentionMonths, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get retention config: %w", err)
	}
	c.Configured = true
	return &c, nil
}

// Upsert replaces the tenant's retention config wholesale, same shape as
// SMTPConfigRepository.Upsert.
func (r *RetentionConfigRepository) Upsert(ctx context.Context, tx pgx.Tx, c *domain.RetentionConfig) error {
	_, err := tx.Exec(ctx, `
		insert into tenant_retention_config (tenant_id, alert_retention_months, incident_retention_months)
		values ($1,$2,$3)
		on conflict (tenant_id) do update set
			alert_retention_months = excluded.alert_retention_months,
			incident_retention_months = excluded.incident_retention_months,
			updated_at = now()`,
		c.TenantID, c.AlertRetentionMonths, c.IncidentRetentionMonths,
	)
	if err != nil {
		return fmt.Errorf("upsert retention config: %w", err)
	}
	return nil
}
