package service_test

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// The full SP-initiated SSO flow (buildServiceProvider, ServeLogin's
// redirect, ServeACS's assertion parsing) needs a real signed SAML
// assertion to exercise past the config-check branch -- that happy path is
// already exercised at the crypto layer by internal/authn/saml_test.go
// (BuildServiceProvider, RedirectToIDP, ParseAssertion). This test only
// covers the deterministic "not configured for this tenant" branch shared
// by all three HTTP-facing methods here.
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
