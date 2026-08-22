package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// RetentionConfigService is Settings -> Retention: how long a closed alert
// or incident stays in the tool before cmd/worker's sweepDataRetention
// permanently deletes it (see that function's own doc comment for what
// "permanently" does and does not touch -- evidence in blob storage is
// never affected, since nothing in this codebase deletes from blobstore in
// the first place).
//
// Unlike every other tenant_*_config service (SMTP/Storage: no row means
// the feature is off), Get here never returns nil -- retention is on by
// default, so an unconfigured tenant still gets a real, usable
// domain.RetentionConfig back, with Configured=false marking it as the
// synthesized default rather than something an admin actually saved.
type RetentionConfigService struct {
	pool *db.Pool
	repo *repository.RetentionConfigRepository
}

func NewRetentionConfigService(pool *db.Pool, repo *repository.RetentionConfigRepository) *RetentionConfigService {
	return &RetentionConfigService{pool: pool, repo: repo}
}

func (s *RetentionConfigService) Get(ctx context.Context, tenantID uuid.UUID) (*domain.RetentionConfig, error) {
	var cfg *domain.RetentionConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.repo.Get(ctx, tx)
		cfg = c
		return err
	})
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return &domain.RetentionConfig{
			TenantID:                tenantID,
			AlertRetentionMonths:    domain.DefaultRetentionMonths,
			IncidentRetentionMonths: domain.DefaultRetentionMonths,
			Configured:              false,
		}, nil
	}
	return cfg, nil
}

type SaveRetentionInput struct {
	AlertRetentionMonths    int
	IncidentRetentionMonths int
}

// Save requires both values non-negative (0 is allowed -- purges a closed
// alert/incident on the very next sweep tick, useful for verifying the
// sweep actually works, not just a theoretical edge case). There is
// deliberately no Delete/reset-to-default method or DELETE route: saving
// {DefaultRetentionMonths, DefaultRetentionMonths} already reaches the same
// effective state, so a separate "delete" would just be a second way to
// get there.
func (s *RetentionConfigService) Save(ctx context.Context, tenantID uuid.UUID, in SaveRetentionInput) error {
	if in.AlertRetentionMonths < 0 || in.IncidentRetentionMonths < 0 {
		return fmt.Errorf("alertRetentionMonths and incidentRetentionMonths must be zero or positive")
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Upsert(ctx, tx, &domain.RetentionConfig{
			TenantID:                tenantID,
			AlertRetentionMonths:    in.AlertRetentionMonths,
			IncidentRetentionMonths: in.IncidentRetentionMonths,
		})
	})
}
