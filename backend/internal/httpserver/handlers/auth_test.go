package handlers_test

import (
	"bytes"
	"encoding/json"
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

// newAuthHandlers wires AuthHandlers against the real seeded default tenant
// (admin@argusops.local / ChangeMe123!, see db/migrations/0013_seed_default_admin.up.sql)
// -- resolveTenant always resolves that single tenant, so login-success
// tests have no other way to reach it than through the actual seed data.
func newAuthHandlers(t *testing.T) *handlers.AuthHandlers {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	issuer := authn.NewIssuer(priv)

	tenants := repository.NewTenantRepository()
	users := repository.NewUserRepository()
	authSvc := service.NewAuthService(pool, tenants, users, repository.NewRefreshTokenRepository(), issuer)
	identityCfg := repository.NewIdentityConfigRepository()
	store := secrets.NewEnvStore()
	ldapSvc := service.NewLDAPAuthService(pool, identityCfg, store, authSvc)
	samlSvc := service.NewSAMLAuthService(pool, identityCfg, store, authSvc)
	// noopSender is defined in smtp_config_test.go (same package) -- these
	// login/refresh/SAML tests never actually exercise password-reset email
	// delivery, so a real Sender isn't needed here.
	smtpSvc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), store, noopSender{})
	passwordResetSvc := service.NewPasswordResetService(pool, repository.NewPasswordResetRepository(), users, smtpSvc, "http://localhost:3000")

	return handlers.NewAuthHandlers(authSvc, ldapSvc, samlSvc, passwordResetSvc)
}

func TestAuthHandlers_LoginLocal(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	t.Run("correct seeded credentials -- 200, mustChangePassword true", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"email": "admin@argusops.local", "password": "ChangeMe123!"})
		req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["token"])
		user := resp["user"].(map[string]any)
		assert.Equal(t, "admin@argusops.local", user["email"])
	})

	t.Run("wrong password -- 401", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"email": "admin@argusops.local", "password": "wrong"})
		req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("malformed body -- 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", bytes.NewReader([]byte("not json")))
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

// TestAuthHandlers_LoginLocal_PerAccountRateLimit guards the distributed
// brute-force gap the per-IP loginLimiter in router.go doesn't cover: many
// wrong-password attempts against the SAME account, from different (or
// spoofable) source IPs, must still get cut off.
func TestAuthHandlers_LoginLocal_PerAccountRateLimit(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	attempt := func(email string) int {
		body, _ := json.Marshal(map[string]string{"email": email, "password": "wrong"})
		req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
		return doRequest(r, req).Code
	}

	for i := 0; i < 10; i++ {
		assert.Equal(t, http.StatusUnauthorized, attempt("admin@argusops.local"), "attempt %d is still within budget", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, attempt("admin@argusops.local"), "11th attempt for this account is rate-limited")

	t.Run("email is normalized before keying, case/whitespace can't bypass the limit", func(t *testing.T) {
		assert.Equal(t, http.StatusTooManyRequests, attempt(" Admin@ArgusOps.local "))
	})

	t.Run("a different account has its own independent budget", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, attempt("someone-else@test.local"))
	})
}

func TestAuthHandlers_LoginLDAP_NotConfigured(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"email": "someone@example.com", "password": "x"})
	req := httptest.NewRequest("POST", "/login/ldap", bytes.NewReader(body))
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "an unconfigured LDAP provider must not leak details -- same generic 401 as bad credentials")
}

func TestAuthHandlers_SAMLRoutes_NotConfigured(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	t.Run("metadata", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/saml/metadata", nil)
		assert.Equal(t, http.StatusNotFound, doRequest(r, req).Code)
	})

	t.Run("login", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/saml/login", nil)
		assert.Equal(t, http.StatusNotFound, doRequest(r, req).Code)
	})

	t.Run("acs", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/saml/acs", nil)
		assert.Equal(t, http.StatusNotFound, doRequest(r, req).Code)
	})
}

func TestAuthHandlers_PasswordReset(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	t.Run("request always returns 204, even for an unknown email", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"email": "no-such-user@test.local"})
		req := httptest.NewRequest("POST", "/password-reset/request", bytes.NewReader(body))
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})

	t.Run("request malformed body -- 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/password-reset/request", bytes.NewReader([]byte("not json")))
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("confirm with an unknown token -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"token": "no-such-token", "newPassword": "NewPassword123!"})
		req := httptest.NewRequest("POST", "/password-reset/confirm", bytes.NewReader(body))
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("confirm malformed body -- 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/password-reset/confirm", bytes.NewReader([]byte("not json")))
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}
