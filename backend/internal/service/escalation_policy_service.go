package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
)

// EscalationPolicyService is Settings -> On-Call Escalation: lets an admin
// configure, per severity, when and how to notify whoever's on shift about
// an alert that's gone unacknowledged. cmd/worker's escalation sweep reads
// these policies directly via SQL (cross-tenant, same BYPASSRLS reasoning
// as its materialized-view refresh / SLA sweep), not through this service.
type EscalationPolicyService struct {
	pool    *db.Pool
	repo    *repository.EscalationPolicyRepository
	secrets secrets.Store
}

func NewEscalationPolicyService(pool *db.Pool, repo *repository.EscalationPolicyRepository, store secrets.Store) *EscalationPolicyService {
	return &EscalationPolicyService{pool: pool, repo: repo, secrets: store}
}

func (s *EscalationPolicyService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.EscalationPolicy, error) {
	var policies []domain.EscalationPolicy
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		policies = v
		return err
	})
	return policies, err
}

func validEscalationChannel(channelType domain.EscalationChannelType) bool {
	switch channelType {
	case domain.EscalationChannelPagerDuty, domain.EscalationChannelSlack, domain.EscalationChannelWebhook:
		return true
	default:
		return false
	}
}

// Save creates or replaces the policy for severity. destination is the
// plaintext PagerDuty routing key / Slack incoming-webhook URL / generic
// webhook URL, resolved through secrets.Store the same as every other
// stored credential in this app; "" on update means keep the existing one
// (same convention as StorageConfigService's SaveS3Input.SecretAccessKey).
func (s *EscalationPolicyService) Save(ctx context.Context, tenantID uuid.UUID, severity domain.Severity, unacknowledgedAfterMinutes int, channelType domain.EscalationChannelType, destination string) (*domain.EscalationPolicy, error) {
	if unacknowledgedAfterMinutes <= 0 {
		return nil, fmt.Errorf("unacknowledgedAfterMinutes must be greater than zero")
	}
	if !validEscalationChannel(channelType) {
		return nil, fmt.Errorf("unknown channel type %q", channelType)
	}

	p := &domain.EscalationPolicy{
		TenantID: tenantID, Severity: severity,
		UnacknowledgedAfterMinutes: unacknowledgedAfterMinutes, ChannelType: channelType,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		ref := ""
		if destination != "" {
			r, err := s.secrets.Put(ctx, tenantID.String(), "escalation:"+string(severity), destination)
			if err != nil {
				return fmt.Errorf("store destination: %w", err)
			}
			ref = r
		} else if existing, err := s.repo.GetBySeverity(ctx, tx, severity); err == nil && existing != nil {
			ref = existing.DestinationSecretRef
		}
		if ref == "" {
			return fmt.Errorf("destination is required for initial configuration")
		}

		p.DestinationSecretRef = ref
		return s.repo.Upsert(ctx, tx, p)
	})
	if err != nil {
		return nil, fmt.Errorf("save escalation policy: %w", err)
	}
	return p, nil
}

func (s *EscalationPolicyService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx, id)
	})
}
