package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestIdentityConfigService_LDAP(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewIdentityConfigService(pool, repository.NewIdentityConfigRepository(), secrets.NewEnvStore())

	t.Run("initial save requires a bind password", func(t *testing.T) {
		err := svc.SaveLDAPConfig(t.Context(), tenantID, service.SaveLDAPConfigInput{
			Host: "ldap.example.com", Port: 636, BindDN: "cn=svc,dc=example,dc=com",
		})
		assert.ErrorContains(t, err, "bindPassword is required")
	})

	require.NoError(t, svc.SaveLDAPConfig(t.Context(), tenantID, service.SaveLDAPConfigInput{
		Host: "ldap.example.com", Port: 636, BindDN: "cn=svc,dc=example,dc=com", BindPassword: "s3cret",
	}))

	cfg, err := svc.GetLDAPConfig(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "ldap.example.com", cfg.Host)
	assert.NotContains(t, cfg.BindPasswordSecretRef, "s3cret", "the plaintext password never lands in the stored ref")

	t.Run("re-saving without a new password keeps the existing secret", func(t *testing.T) {
		require.NoError(t, svc.SaveLDAPConfig(t.Context(), tenantID, service.SaveLDAPConfigInput{
			Host: "ldap2.example.com", Port: 389, BindDN: "cn=svc,dc=example,dc=com",
		}))
		got, err := svc.GetLDAPConfig(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, "ldap2.example.com", got.Host)
		assert.Equal(t, cfg.BindPasswordSecretRef, got.BindPasswordSecretRef)
	})
}

func TestIdentityConfigService_SAML(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewIdentityConfigService(pool, repository.NewIdentityConfigRepository(), secrets.NewEnvStore())

	t.Run("no config yet", func(t *testing.T) {
		cfg, err := svc.GetSAMLConfig(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, cfg)
	})

	metadataURL := "https://idp.example.com/metadata"
	require.NoError(t, svc.SaveSAMLConfig(t.Context(), tenantID, service.SaveSAMLConfigInput{
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
		require.NoError(t, svc.SaveSAMLConfig(t.Context(), tenantID, service.SaveSAMLConfigInput{
			IDPMetadataURL: &newURL,
			SPEntityID:     "https://argusops.example/saml/metadata",
			ACSURL:         "https://argusops.example/auth/saml/acs",
		}))
		got, err := svc.GetSAMLConfig(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, newURL, *got.IDPMetadataURL)
		assert.Equal(t, firstCertRef, got.SPCertSecretRef, "the IdP already trusts this certificate")
	})
}
