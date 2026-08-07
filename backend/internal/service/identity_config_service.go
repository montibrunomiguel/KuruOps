package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
)

// IdentityConfigService is what Settings -> (a not-yet-designed) Identity
// Providers panel would call to let an admin wire up their own LDAP
// directory or SAML IdP — the config AuthService/LDAPAuthService/
// SAMLAuthService read at login time.
type IdentityConfigService struct {
	pool    *db.Pool
	repo    *repository.IdentityConfigRepository
	secrets secrets.Store

	// onSAMLConfigSaved fires after a successful SaveSAMLConfig -- wired to
	// SAMLAuthService.InvalidateMetadataCache (see cmd/api/main.go) via a
	// setter rather than a constructor param, since the two services would
	// otherwise depend on each other in a cycle (SAMLAuthService already
	// depends on *repository.IdentityConfigRepository, not this service).
	onSAMLConfigSaved func(tenantID uuid.UUID)
}

func NewIdentityConfigService(pool *db.Pool, repo *repository.IdentityConfigRepository, store secrets.Store) *IdentityConfigService {
	return &IdentityConfigService{pool: pool, repo: repo, secrets: store}
}

// SetOnSAMLConfigSaved registers a callback invoked after every successful
// SaveSAMLConfig, so a cached IdP metadata document doesn't keep serving a
// tenant's old IdP metadata URL/XML for up to samlMetadataTTL after they
// change it.
func (s *IdentityConfigService) SetOnSAMLConfigSaved(fn func(tenantID uuid.UUID)) {
	s.onSAMLConfigSaved = fn
}

type SaveLDAPConfigInput struct {
	Host           string
	Port           int
	UseTLS         bool
	BindDN         string
	BindPassword   string // plaintext; "" on update means keep existing
	UserBaseDN     string
	UserFilter     string
	GroupBaseDN    string
	GroupAttribute string
}

func (s *IdentityConfigService) SaveLDAPConfig(ctx context.Context, tenantID uuid.UUID, in SaveLDAPConfigInput) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		ref := ""
		if in.BindPassword != "" {
			r, err := s.secrets.Put(ctx, tenantID.String(), "ldap-bind-password", in.BindPassword)
			if err != nil {
				return fmt.Errorf("store bind password: %w", err)
			}
			ref = r
		} else if existing, err := s.repo.GetLDAPConfig(ctx, tx); err == nil && existing != nil {
			ref = existing.BindPasswordSecretRef
		}
		if ref == "" {
			return fmt.Errorf("bindPassword is required for initial configuration")
		}

		return s.repo.UpsertLDAPConfig(ctx, tx, &domain.LDAPConfig{
			TenantID: tenantID, Host: in.Host, Port: in.Port, UseTLS: in.UseTLS,
			BindDN: in.BindDN, BindPasswordSecretRef: ref,
			UserBaseDN: in.UserBaseDN, UserFilter: in.UserFilter,
			GroupBaseDN: in.GroupBaseDN, GroupAttribute: in.GroupAttribute,
		})
	})
}

func (s *IdentityConfigService) GetLDAPConfig(ctx context.Context, tenantID uuid.UUID) (*domain.LDAPConfig, error) {
	var cfg *domain.LDAPConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.repo.GetLDAPConfig(ctx, tx)
		cfg = c
		return err
	})
	return cfg, err
}

type SaveSAMLConfigInput struct {
	IDPMetadataURL *string
	IDPMetadataXML *string
	ACSURL         string
	SPEntityID     string
	GroupAttribute *string
}

// SaveSAMLConfig generates a new SP signing keypair the first time a
// tenant configures SAML (authn.GenerateSPKeyPair) — the admin then
// downloads the metadata from GET /auth/saml/metadata to register with
// their IdP. Re-saving config (e.g. changing the IdP metadata URL) keeps
// the existing keypair rather than rotating it, since the IdP has already
// trusted that certificate.
func (s *IdentityConfigService) SaveSAMLConfig(ctx context.Context, tenantID uuid.UUID, in SaveSAMLConfigInput) error {
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.GetSAMLConfig(ctx, tx)
		if err != nil {
			return fmt.Errorf("load existing saml config: %w", err)
		}

		certRef, keyRef := "", ""
		if existing != nil {
			certRef, keyRef = existing.SPCertSecretRef, existing.SPKeySecretRef
		} else {
			certPEM, keyPEM, err := authn.GenerateSPKeyPair(in.SPEntityID)
			if err != nil {
				return fmt.Errorf("generate sp keypair: %w", err)
			}
			certRef, err = s.secrets.Put(ctx, tenantID.String(), "saml-sp-cert", certPEM)
			if err != nil {
				return fmt.Errorf("store sp cert: %w", err)
			}
			keyRef, err = s.secrets.Put(ctx, tenantID.String(), "saml-sp-key", keyPEM)
			if err != nil {
				return fmt.Errorf("store sp key: %w", err)
			}
		}

		return s.repo.UpsertSAMLConfig(ctx, tx, &domain.SAMLConfig{
			TenantID: tenantID, IDPMetadataURL: in.IDPMetadataURL, IDPMetadataXML: in.IDPMetadataXML,
			SPEntityID: in.SPEntityID, ACSURL: in.ACSURL,
			SPCertSecretRef: certRef, SPKeySecretRef: keyRef, GroupAttribute: in.GroupAttribute,
		})
	})
	if err == nil && s.onSAMLConfigSaved != nil {
		s.onSAMLConfigSaved(tenantID)
	}
	return err
}

func (s *IdentityConfigService) GetSAMLConfig(ctx context.Context, tenantID uuid.UUID) (*domain.SAMLConfig, error) {
	var cfg *domain.SAMLConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.repo.GetSAMLConfig(ctx, tx)
		cfg = c
		return err
	})
	return cfg, err
}
