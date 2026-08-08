package repository_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestIdentityConfigRepository_LDAP(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIdentityConfigRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("no config yet returns nil, not an error", func(t *testing.T) {
		got, err := repo.GetLDAPConfig(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	cfg := &domain.LDAPConfig{
		TenantID:              tenantID,
		Host:                  "ldap.example.com",
		Port:                  636,
		UseTLS:                true,
		BindDN:                "cn=svc,dc=example,dc=com",
		BindPasswordSecretRef: "secret://ldap-bind",
		UserBaseDN:            "ou=users,dc=example,dc=com",
		UserFilter:            "(uid=%s)",
		GroupBaseDN:           "ou=groups,dc=example,dc=com",
		GroupAttribute:        "memberOf",
	}
	require.NoError(t, repo.UpsertLDAPConfig(t.Context(), tx, cfg))

	t.Run("get after insert", func(t *testing.T) {
		got, err := repo.GetLDAPConfig(t.Context(), tx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "ldap.example.com", got.Host)
		assert.Equal(t, 636, got.Port)
		assert.True(t, got.UseTLS)
	})

	t.Run("upsert on conflict updates in place", func(t *testing.T) {
		cfg.Host = "ldap2.example.com"
		cfg.Port = 389
		require.NoError(t, repo.UpsertLDAPConfig(t.Context(), tx, cfg))

		got, err := repo.GetLDAPConfig(t.Context(), tx)
		require.NoError(t, err)
		assert.Equal(t, "ldap2.example.com", got.Host)
		assert.Equal(t, 389, got.Port)
	})

	t.Run("delete removes the config", func(t *testing.T) {
		require.NoError(t, repo.DeleteLDAPConfig(t.Context(), tx))

		got, err := repo.GetLDAPConfig(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("delete is a no-op when nothing is configured", func(t *testing.T) {
		require.NoError(t, repo.DeleteLDAPConfig(t.Context(), tx))
	})
}

func TestIdentityConfigRepository_SAML(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIdentityConfigRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("no config yet returns nil, not an error", func(t *testing.T) {
		got, err := repo.GetSAMLConfig(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	metadataURL := "https://idp.example.com/metadata"
	groupAttr := "groups"
	cfg := &domain.SAMLConfig{
		TenantID:        tenantID,
		IDPMetadataURL:  &metadataURL,
		SPEntityID:      "https://argusops.example/saml/metadata",
		ACSURL:          "https://argusops.example/auth/saml/acs",
		SPCertSecretRef: "secret://saml-cert",
		SPKeySecretRef:  "secret://saml-key",
		GroupAttribute:  &groupAttr,
	}
	require.NoError(t, repo.UpsertSAMLConfig(t.Context(), tx, cfg))

	got, err := repo.GetSAMLConfig(t.Context(), tx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "https://argusops.example/saml/metadata", got.SPEntityID)
	require.NotNil(t, got.IDPMetadataURL)
	assert.Equal(t, metadataURL, *got.IDPMetadataURL)

	t.Run("delete removes the config", func(t *testing.T) {
		require.NoError(t, repo.DeleteSAMLConfig(t.Context(), tx))

		got, err := repo.GetSAMLConfig(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("delete is a no-op when nothing is configured", func(t *testing.T) {
		require.NoError(t, repo.DeleteSAMLConfig(t.Context(), tx))
	})
}
