package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
)

// TenantRepository is the one repository that does NOT take a pgx.Tx from
// Pool.WithTenant -- tenants is not RLS-scoped (see the comment at the
// bottom of db/migrations/0008_row_level_security.up.sql), so its methods
// take the pool directly. This is the sanctioned exception; every other
// repository in this codebase must go through WithTenant.
type TenantRepository struct{}

func NewTenantRepository() *TenantRepository {
	return &TenantRepository{}
}

// GetDefault resolves "the" tenant, before app.tenant_id can be set — this
// is the login-time equivalent of WebhookRepository.ResolveToken. ArgusOps
// is single-instance software (see 0013_seed_default_admin.up.sql): there
// is exactly one row in `tenants`, seeded on first migrate, and every login
// flow (local/LDAP/SAML) resolves it automatically instead of asking for a
// company name. The tenant_id/RLS plumbing everywhere else stays in place
// on purpose -- it's what would let this grow into real multi-tenant SaaS
// later without a schema rewrite -- but nothing in the API surface exposes
// tenant selection today.
func (r *TenantRepository) GetDefault(ctx context.Context, pool *db.Pool) (*domain.Tenant, error) {
	var t domain.Tenant
	err := pool.QueryRow(ctx, `select id, name, slug, created_at from tenants order by created_at asc limit 1`).
		Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get default tenant: %w", err)
	}
	return &t, nil
}

// GetTimezone/SetTimezone back the on-call schedule's "resolve who's on
// call right now" logic (see OnCallScheduleService.ResolveCurrentAnalyst) --
// pool-direct for the same reason as GetDefault: tenants carries no RLS.
func (r *TenantRepository) GetTimezone(ctx context.Context, pool *db.Pool, tenantID uuid.UUID) (string, error) {
	var tz string
	err := pool.QueryRow(ctx, `select timezone from tenants where id = $1`, tenantID).Scan(&tz)
	if err != nil {
		return "", fmt.Errorf("get tenant timezone: %w", err)
	}
	return tz, nil
}

func (r *TenantRepository) SetTimezone(ctx context.Context, pool *db.Pool, tenantID uuid.UUID, timezone string) error {
	_, err := pool.Exec(ctx, `update tenants set timezone = $2 where id = $1`, tenantID, timezone)
	return err
}
