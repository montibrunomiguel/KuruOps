package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// fakeIdentityConfigRepo lets a test fail a specific repo call on demand --
// IdentityConfigService takes an interface (not the concrete
// *repository.IdentityConfigRepository) specifically so this is possible.
// See fakeStorageConfigRepo (storage_config_service_test.go) for the fuller
// version of this reasoning.
type fakeIdentityConfigRepo struct {
	getLDAPErr    error
	upsertLDAPErr error
	deleteLDAPErr error
	getSAMLErr    error
	upsertSAMLErr error
	deleteSAMLErr error
}

func (f *fakeIdentityConfigRepo) GetLDAPConfig(context.Context, pgx.Tx) (*domain.LDAPConfig, error) {
	return nil, f.getLDAPErr
}
func (f *fakeIdentityConfigRepo) UpsertLDAPConfig(context.Context, pgx.Tx, *domain.LDAPConfig) error {
	return f.upsertLDAPErr
}
func (f *fakeIdentityConfigRepo) DeleteLDAPConfig(context.Context, pgx.Tx) error {
	return f.deleteLDAPErr
}
func (f *fakeIdentityConfigRepo) GetSAMLConfig(context.Context, pgx.Tx) (*domain.SAMLConfig, error) {
	return nil, f.getSAMLErr
}
func (f *fakeIdentityConfigRepo) UpsertSAMLConfig(context.Context, pgx.Tx, *domain.SAMLConfig) error {
	return f.upsertSAMLErr
}
func (f *fakeIdentityConfigRepo) DeleteSAMLConfig(context.Context, pgx.Tx) error {
	return f.deleteSAMLErr
}

func TestIdentityConfigService_LDAP(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewIdentityConfigService(pool, repository.NewIdentityConfigRepository(), secrets.NewEnvStore(), auditRepo)

	t.Run("initial save requires a bind password", func(t *testing.T) {
		err := svc.SaveLDAPConfig(t.Context(), tenantID, actorID, service.SaveLDAPConfigInput{
			Host: "ldap.example.com", Port: 636, BindDN: "cn=svc,dc=example,dc=com",
		})
		assert.ErrorContains(t, err, "bindPassword is required")
	})

	require.NoError(t, svc.SaveLDAPConfig(t.Context(), tenantID, actorID, service.SaveLDAPConfigInput{
		Host: "ldap.example.com", Port: 636, BindDN: "cn=svc,dc=example,dc=com", BindPassword: "s3cret",
	}))

	cfg, err := svc.GetLDAPConfig(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "ldap.example.com", cfg.Host)
	assert.NotContains(t, cfg.BindPasswordSecretRef, "s3cret", "the plaintext password never lands in the stored ref")

	t.Run("re-saving without a new password keeps the existing secret", func(t *testing.T) {
		require.NoError(t, svc.SaveLDAPConfig(t.Context(), tenantID, actorID, service.SaveLDAPConfigInput{
			Host: "ldap2.example.com", Port: 389, BindDN: "cn=svc,dc=example,dc=com",
		}))
		got, err := svc.GetLDAPConfig(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, "ldap2.example.com", got.Host)
		assert.Equal(t, cfg.BindPasswordSecretRef, got.BindPasswordSecretRef)
	})

	t.Run("delete removes the config", func(t *testing.T) {
		require.NoError(t, svc.DeleteLDAPConfig(t.Context(), tenantID, actorID))
		got, err := svc.GetLDAPConfig(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("save and delete each record an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "identity-config", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "save-ldap")
		assert.Contains(t, actions, "delete-ldap")
	})
}

func TestIdentityConfigService_SAML(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewIdentityConfigService(pool, repository.NewIdentityConfigRepository(), secrets.NewEnvStore(), auditRepo)

	t.Run("no config yet", func(t *testing.T) {
		cfg, err := svc.GetSAMLConfig(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, cfg)
	})

	metadataURL := "https://idp.example.com/metadata"
	require.NoError(t, svc.SaveSAMLConfig(t.Context(), tenantID, actorID, service.SaveSAMLConfigInput{
		IDPMetadataURL: &metadataURL,
		SPEntityID:     "https://argusops.example/saml/metadata",
		ACSURL:         "https://argusops.example/auth/saml/acs",
	}))

	cfg, err := svc.GetSAMLConfig(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	firstCertRef := cfg.SPCertSecretRef
	assert.NotEmpty(t, firstCertRef, "a keypair is generated on first save")

	t.Run("re-saving keeps the existing SP keypair rather than rotating it", func(t *testing.T) {
		newURL := "https://idp2.example.com/metadata"
		require.NoError(t, svc.SaveSAMLConfig(t.Context(), tenantID, actorID, service.SaveSAMLConfigInput{
			IDPMetadataURL: &newURL,
			SPEntityID:     "https://argusops.example/saml/metadata",
			ACSURL:         "https://argusops.example/auth/saml/acs",
		}))
		got, err := svc.GetSAMLConfig(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, newURL, *got.IDPMetadataURL)
		assert.Equal(t, firstCertRef, got.SPCertSecretRef, "the IdP already trusts this certificate")
	})

	t.Run("delete removes the config", func(t *testing.T) {
		require.NoError(t, svc.DeleteSAMLConfig(t.Context(), tenantID, actorID))
		got, err := svc.GetSAMLConfig(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("delete fires onSAMLConfigChanged to invalidate any cached IdP metadata", func(t *testing.T) {
		var invalidated bool
		svc.SetOnSAMLConfigChanged(func(id uuid.UUID) {
			if id == tenantID {
				invalidated = true
			}
		})
		require.NoError(t, svc.DeleteSAMLConfig(t.Context(), tenantID, actorID))
		assert.True(t, invalidated)
	})

	t.Run("save and delete each record an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "identity-config", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "save-saml")
		assert.Contains(t, actions, "delete-saml")
	})
}

// TestIdentityConfigService_RepoErrors exercises each Save/Delete method's
// "load existing config to build the audit diff, then persist"
// error-wrapping branches -- unreachable via a real Postgres integration
// test.
func TestIdentityConfigService_RepoErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	newSvc := func(fake *fakeIdentityConfigRepo) *service.IdentityConfigService {
		return service.NewIdentityConfigService(pool, fake, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
	}

	t.Run("SaveLDAPConfig wraps a GetLDAPConfig failure", func(t *testing.T) {
		err := newSvc(&fakeIdentityConfigRepo{getLDAPErr: errors.New("get boom")}).SaveLDAPConfig(t.Context(), tenantID, actorID, service.SaveLDAPConfigInput{
			Host: "ldap.example.com", Port: 636, BindDN: "cn=svc", BindPassword: "s3cret",
		})
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("SaveLDAPConfig wraps an UpsertLDAPConfig failure", func(t *testing.T) {
		err := newSvc(&fakeIdentityConfigRepo{upsertLDAPErr: errors.New("upsert boom")}).SaveLDAPConfig(t.Context(), tenantID, actorID, service.SaveLDAPConfigInput{
			Host: "ldap.example.com", Port: 636, BindDN: "cn=svc", BindPassword: "s3cret",
		})
		assert.ErrorContains(t, err, "upsert boom")
	})

	t.Run("DeleteLDAPConfig wraps a GetLDAPConfig failure", func(t *testing.T) {
		err := newSvc(&fakeIdentityConfigRepo{getLDAPErr: errors.New("get boom")}).DeleteLDAPConfig(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("DeleteLDAPConfig wraps a DeleteLDAPConfig failure", func(t *testing.T) {
		err := newSvc(&fakeIdentityConfigRepo{deleteLDAPErr: errors.New("delete boom")}).DeleteLDAPConfig(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "delete boom")
	})

	t.Run("SaveSAMLConfig wraps a GetSAMLConfig failure", func(t *testing.T) {
		err := newSvc(&fakeIdentityConfigRepo{getSAMLErr: errors.New("get boom")}).SaveSAMLConfig(t.Context(), tenantID, actorID, service.SaveSAMLConfigInput{
			SPEntityID: "https://argusops.example/saml/metadata", ACSURL: "https://argusops.example/auth/saml/acs",
		})
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("DeleteSAMLConfig wraps a GetSAMLConfig failure", func(t *testing.T) {
		err := newSvc(&fakeIdentityConfigRepo{getSAMLErr: errors.New("get boom")}).DeleteSAMLConfig(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("DeleteSAMLConfig wraps a DeleteSAMLConfig failure", func(t *testing.T) {
		err := newSvc(&fakeIdentityConfigRepo{deleteSAMLErr: errors.New("delete boom")}).DeleteSAMLConfig(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "delete boom")
	})
}
