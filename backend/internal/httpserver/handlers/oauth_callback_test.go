package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// newOAuthCallbackHandlers wires OAuthCallbackHandlers against the real
// seeded default tenant, same reasoning newAuthHandlers documents --
// resolveTenant-style callback handlers always resolve that one tenant,
// so there's no other way to reach a non-error path in a test.
func newOAuthCallbackHandlers(t *testing.T) (*handlers.OAuthCallbackHandlers, *service.StorageConfigService) {
	t.Helper()
	pool := testutil.RequireTestDB(t)

	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(),
		repository.NewRefreshTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository()), authn.NewIssuer(priv))

	oauthStates := service.NewOAuthStateService(pool, repository.NewOAuthStateRepository())
	storageSvc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir(),
		oauthStates, "test-client-id", "test-client-secret", "https://argusops.example/auth/oauth/gdrive/callback")

	return handlers.NewOAuthCallbackHandlers(authSvc, storageSvc, "https://argusops.example"), storageSvc
}

func TestOAuthCallbackHandlers_GDrive(t *testing.T) {
	h, _ := newOAuthCallbackHandlers(t)
	r := newRouter(h.Routes)

	t.Run("admin declined consent -- redirects back with the provider's error, never 500s", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/gdrive/callback?error=access_denied", nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusFound, rec.Code)
		loc := rec.Header().Get("Location")
		assert.Contains(t, loc, "/settings/storage")
		assert.Contains(t, loc, "gdrive_error=access_denied")
	})

	t.Run("invalid state -- redirects back with an error, not a bare HTTP error page", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/gdrive/callback?code=some-code&state=not-a-real-state", nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusFound, rec.Code)
		loc := rec.Header().Get("Location")
		assert.Contains(t, loc, "/settings/storage")
		assert.Contains(t, loc, "gdrive_error=")
	})
}
