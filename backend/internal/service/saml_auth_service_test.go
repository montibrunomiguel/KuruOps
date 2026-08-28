package service_test

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// This test only covers the deterministic "not configured for this tenant"
// branch shared by all three HTTP-facing methods here. See
// TestSAMLAuthService_Configured below for buildServiceProvider,
// resolveIDPMetadata's cache, ServeMetadata's and ServeLogin's success
// paths, and ServeACS's post-buildServiceProvider assertion-rejection
// branch. ServeACS's actual assertion-parsing *success* path (a real signed
// SAML Response) still isn't covered here -- that needs an IdP-side
// assertion signer this codebase doesn't have test infrastructure for; see
// internal/authn/saml_test.go's TestParseAssertion_RejectsMissingOrInvalidResponse
// for the same boundary at the crypto layer.
func TestSAMLAuthService_NotConfigured(t *testing.T) {
	pool, authSvc := newAuthService(t)
	tenantID := testutil.NewTenant(t)
	samlSvc := service.NewSAMLAuthService(pool, repository.NewIdentityConfigRepository(), secrets.NewEnvStore(), authSvc)

	t.Run("ServeMetadata", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/auth/saml/metadata", nil)
		rec := httptest.NewRecorder()
		samlSvc.ServeMetadata(t.Context(), tenantID, rec, req)
		assert.Equal(t, 404, rec.Code)
	})

	t.Run("ServeLogin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/auth/saml/login", nil)
		rec := httptest.NewRecorder()
		samlSvc.ServeLogin(t.Context(), tenantID, rec, req)
		assert.Equal(t, 404, rec.Code)
	})

	t.Run("ServeACS", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/auth/saml/acs", nil)
		rec := httptest.NewRecorder()
		samlSvc.ServeACS(t.Context(), tenantID, rec, req)
		assert.Equal(t, 404, rec.Code)
	})
}

func TestSAMLAuthService_Configured(t *testing.T) {
	pool, authSvc := newAuthService(t)
	tenantID := testutil.NewTenant(t)
	identityCfg := repository.NewIdentityConfigRepository()
	store := secrets.NewEnvStore()

	spCert, spKey, err := authn.GenerateSPKeyPair("https://kuruops.example/saml/metadata")
	require.NoError(t, err)
	idpCert, _, err := authn.GenerateSPKeyPair("https://idp.example/metadata")
	require.NoError(t, err)
	metadataXML := testutil.IDPMetadataXML(t, "https://idp.example/metadata", idpCert)

	certRef, err := store.Put(t.Context(), tenantID.String(), "saml-sp-cert", spCert)
	require.NoError(t, err)
	keyRef, err := store.Put(t.Context(), tenantID.String(), "saml-sp-key", spKey)
	require.NoError(t, err)

	require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		return identityCfg.UpsertSAMLConfig(t.Context(), tx, &domain.SAMLConfig{
			TenantID: tenantID, IDPMetadataXML: &metadataXML,
			SPEntityID: "https://kuruops.example/saml/metadata", ACSURL: "https://kuruops.example/auth/saml/acs",
			SPCertSecretRef: certRef, SPKeySecretRef: keyRef,
		})
	}))

	samlSvc := service.NewSAMLAuthService(pool, identityCfg, store, authSvc)

	t.Run("ServeMetadata succeeds and builds the service provider", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/auth/saml/metadata", nil)
		rec := httptest.NewRecorder()
		samlSvc.ServeMetadata(t.Context(), tenantID, rec, req)
		require.Equal(t, 200, rec.Code)
		assert.Contains(t, rec.Body.String(), "https://kuruops.example/saml/metadata")
	})

	t.Run("a second call within the TTL reuses the cached IdP metadata", func(t *testing.T) {
		// Same assertion as above -- this just exercises resolveIDPMetadata's
		// cache-hit branch (fetchedAt within samlMetadataTTL) instead of
		// re-parsing the XML. Behaviorally identical output either way with
		// inline XML, so there's nothing further to assert than "still works".
		req := httptest.NewRequest("GET", "/auth/saml/metadata", nil)
		rec := httptest.NewRecorder()
		samlSvc.ServeMetadata(t.Context(), tenantID, rec, req)
		assert.Equal(t, 200, rec.Code)
	})

	t.Run("InvalidateMetadataCache forces the next call to re-resolve", func(t *testing.T) {
		samlSvc.InvalidateMetadataCache(tenantID)
		req := httptest.NewRequest("GET", "/auth/saml/metadata", nil)
		rec := httptest.NewRecorder()
		samlSvc.ServeMetadata(t.Context(), tenantID, rec, req)
		assert.Equal(t, 200, rec.Code)
	})

	t.Run("ServeLogin redirects to the IdP with a RelayState", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/auth/saml/login", nil)
		rec := httptest.NewRecorder()
		samlSvc.ServeLogin(t.Context(), tenantID, rec, req)
		require.Equal(t, 302, rec.Code)
		location, err := url.Parse(rec.Header().Get("Location"))
		require.NoError(t, err)
		assert.NotEmpty(t, location.Query().Get("RelayState"))
	})

	t.Run("ServeACS builds the service provider but rejects a missing assertion", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/auth/saml/acs", strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		samlSvc.ServeACS(t.Context(), tenantID, rec, req)
		assert.Equal(t, 401, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid saml assertion")
	})
}

func TestSAMLAuthService_Configured_UnresolvableSPCert(t *testing.T) {
	pool, authSvc := newAuthService(t)
	tenantID := testutil.NewTenant(t)
	identityCfg := repository.NewIdentityConfigRepository()
	store := secrets.NewEnvStore()

	_, spKey, err := authn.GenerateSPKeyPair("https://kuruops.example/saml/metadata")
	require.NoError(t, err)
	idpCert, _, err := authn.GenerateSPKeyPair("https://idp.example/metadata")
	require.NoError(t, err)
	metadataXML := testutil.IDPMetadataXML(t, "https://idp.example/metadata", idpCert)

	keyRef, err := store.Put(t.Context(), tenantID.String(), "saml-sp-key", spKey)
	require.NoError(t, err)

	require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		return identityCfg.UpsertSAMLConfig(t.Context(), tx, &domain.SAMLConfig{
			TenantID: tenantID, IDPMetadataXML: &metadataXML,
			SPEntityID: "https://kuruops.example/saml/metadata", ACSURL: "https://kuruops.example/auth/saml/acs",
			// secrets.EnvStore.Resolve on an unknown ref returns "", nil
			// (it never errors), so this doesn't reach buildServiceProvider's
			// "resolve sp cert" wrapped-error branch -- it instead surfaces one
			// step later, when authn.BuildServiceProviderFromMetadata fails to
			// PEM-decode the empty cert string. Either way ServeMetadata's
			// caller-facing behavior is the same: 500 with the error message.
			SPCertSecretRef: "no-such-cert-ref", SPKeySecretRef: keyRef,
		})
	}))

	samlSvc := service.NewSAMLAuthService(pool, identityCfg, store, authSvc)

	req := httptest.NewRequest("GET", "/auth/saml/metadata", nil)
	rec := httptest.NewRecorder()
	samlSvc.ServeMetadata(t.Context(), tenantID, rec, req)
	assert.Equal(t, 500, rec.Code)
}
