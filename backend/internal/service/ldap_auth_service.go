package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
)

// LDAPAuthService is the LDAP half of "local + LDAP + SAML" — resolves the
// tenant's directory config, performs the bind-based credential check
// (internal/authn/ldap.go), and hands the result to AuthService for the
// same just-in-time provisioning + JWT issuance SAML uses.
type LDAPAuthService struct {
	pool        *db.Pool
	identityCfg *repository.IdentityConfigRepository
	secrets     secrets.Store
	auth        *AuthService
}

func NewLDAPAuthService(pool *db.Pool, identityCfg *repository.IdentityConfigRepository, store secrets.Store, auth *AuthService) *LDAPAuthService {
	return &LDAPAuthService{pool: pool, identityCfg: identityCfg, secrets: store, auth: auth}
}

func (s *LDAPAuthService) Login(ctx context.Context, tenantID uuid.UUID, email, password string) (*domain.User, string, string, error) {
	var cfg *domain.LDAPConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.identityCfg.GetLDAPConfig(ctx, tx)
		cfg = c
		return err
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("load ldap config: %w", err)
	}
	if cfg == nil {
		return nil, "", "", fmt.Errorf("ldap is not configured for this tenant")
	}

	bindPassword, err := s.secrets.Resolve(ctx, cfg.BindPasswordSecretRef)
	if err != nil {
		return nil, "", "", fmt.Errorf("resolve ldap bind password: %w", err)
	}

	result, err := authn.AuthenticateLDAP(authn.LDAPParams{
		Host: cfg.Host, Port: cfg.Port, UseTLS: cfg.UseTLS,
		BindDN: cfg.BindDN, BindPassword: bindPassword,
		UserBaseDN: cfg.UserBaseDN, UserFilter: cfg.UserFilter,
		GroupBaseDN: cfg.GroupBaseDN, GroupAttribute: cfg.GroupAttribute,
	}, email, password)
	if err != nil {
		return nil, "", "", fmt.Errorf("ldap authentication failed: %w", err)
	}

	// The bound DN, not the login email, is the durable external identity —
	// an email can be reassigned in the directory, a DN is structural.
	return s.auth.ProvisionFederated(ctx, tenantID, domain.AuthProviderLDAP, result.DN, email, result.Name, result.Groups)
}
