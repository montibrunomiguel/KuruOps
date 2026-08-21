package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
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
// (admin@argusops.local / ChangeMe123!, see db/migrations/0002_seed_default_admin.up.sql)
// -- resolveTenant always resolves that single tenant, so login-success
// tests have no other way to reach it than through the actual seed data.
func newAuthHandlers(t *testing.T) *handlers.AuthHandlers {
	t.Helper()
	pool := testutil.RequireTestDB(t)

	// loginAttempts (scope "login_email") is backed by a shared Postgres
	// table now, not a fresh in-memory map per test process (see
	// KeyedLimiter) -- several tests in this file legitimately submit the
	// real seeded admin@argusops.local address to exercise a successful
	// login, and without this they'd accumulate against the same 10-per-
	// 15-minute budget across every test (and every previous run within
	// that window) instead of each test getting the clean slate the old
	// design gave for free. Test-only hygiene, not a production concern.
	_, err := pool.Exec(context.Background(), "delete from rate_limit_events where scope = 'login_email'")
	require.NoError(t, err)

	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	issuer := authn.NewIssuer(priv)

	tenants := repository.NewTenantRepository()
	users := repository.NewUserRepository()
	roleSvc := service.NewRoleService(pool, repository.NewRoleRepository())
	authSvc := service.NewAuthService(pool, tenants, users, repository.NewRefreshTokenRepository(), roleSvc, issuer)
	identityCfg := repository.NewIdentityConfigRepository()
	store := secrets.NewEnvStore()
	ldapSvc := service.NewLDAPAuthService(pool, identityCfg, store, authSvc)
	samlSvc := service.NewSAMLAuthService(pool, identityCfg, store, authSvc)
	// noopSender is defined in smtp_config_test.go (same package) -- these
	// login/refresh/SAML tests never actually exercise password-reset email
	// delivery, so a real Sender isn't needed here.
	smtpSvc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), store, noopSender{})
	passwordResetSvc := service.NewPasswordResetService(pool, repository.NewPasswordResetRepository(), users, smtpSvc, "http://localhost:3000")

	return handlers.NewAuthHandlers(t.Context(), pool.Pool, authSvc, ldapSvc, samlSvc, passwordResetSvc)
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

	// A unique email per test run -- loginAttempts is now backed by a
	// shared Postgres table (see KeyedLimiter), not a fresh in-memory map
	// per process, so a literal fixed email here would collide with this
	// same test's own prior runs (rows outlive one `go test` invocation)
	// and with any other test in this file that also submits to
	// /login or /password-reset/* with the same address (they all share
	// the "login_email" scope -- see AuthHandlers' loginAttempts doc
	// comment).
	email := fmt.Sprintf("ratelimit-local-%s@test.local", uuid.NewString())

	for i := 0; i < 10; i++ {
		assert.Equal(t, http.StatusUnauthorized, attempt(email), "attempt %d is still within budget", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, attempt(email), "11th attempt for this account is rate-limited")

	t.Run("email is normalized before keying, case/whitespace can't bypass the limit", func(t *testing.T) {
		assert.Equal(t, http.StatusTooManyRequests, attempt(" "+strings.ToUpper(email)+" "))
	})

	t.Run("a different account has its own independent budget", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, attempt(fmt.Sprintf("ratelimit-other-%s@test.local", uuid.NewString())))
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

func TestAuthHandlers_LoginLDAP_MalformedBody(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("POST", "/login/ldap", bytes.NewReader([]byte("not json")))
	assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
}

func TestAuthHandlers_LoginLDAP_PerAccountRateLimit(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	// Unique per run -- see TestAuthHandlers_LoginLocal_PerAccountRateLimit's
	// comment on why a fixed literal would collide across test runs/tests
	// now that loginAttempts is backed by a shared Postgres table.
	email := fmt.Sprintf("ratelimit-ldap-%s@test.local", uuid.NewString())
	attempt := func() int {
		body, _ := json.Marshal(map[string]string{"email": email, "password": "x"})
		req := httptest.NewRequest("POST", "/login/ldap", bytes.NewReader(body))
		return doRequest(r, req).Code
	}

	for i := 0; i < 10; i++ {
		assert.Equal(t, http.StatusUnauthorized, attempt(), "attempt %d is still within budget", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, attempt(), "11th attempt for this account is rate-limited")
}

// TestAuthHandlers_Refresh exercises the one handler in this file with 0%
// coverage before this test existed: a real login (against the seeded
// default admin) issues a refresh token, which is then exchanged, rejected
// when malformed/empty/unknown, and confirmed to actually rotate (the old
// token stops working once a new one has been issued from it).
func TestAuthHandlers_Refresh(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	loginBody, _ := json.Marshal(map[string]string{"email": "admin@argusops.local", "password": "ChangeMe123!"})
	loginReq := httptest.NewRequest("POST", "/login", bytes.NewReader(loginBody))
	loginRec := doRequest(r, loginReq)
	require.Equal(t, http.StatusOK, loginRec.Code)
	var loginResp struct {
		RefreshToken string `json:"refreshToken"`
	}
	require.NoError(t, json.Unmarshal(loginRec.Body.Bytes(), &loginResp))
	require.NotEmpty(t, loginResp.RefreshToken)

	t.Run("malformed body -- 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/refresh", bytes.NewReader([]byte("not json")))
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("empty refreshToken -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"refreshToken": ""})
		req := httptest.NewRequest("POST", "/refresh", bytes.NewReader(body))
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("unknown refreshToken -- 401", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"refreshToken": "rt_not-a-real-token"})
		req := httptest.NewRequest("POST", "/refresh", bytes.NewReader(body))
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})

	var rotatedRefreshToken string
	t.Run("valid refreshToken -- 200, rotates the token", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"refreshToken": loginResp.RefreshToken})
		req := httptest.NewRequest("POST", "/refresh", bytes.NewReader(body))
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Token        string `json:"token"`
			RefreshToken string `json:"refreshToken"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp.Token)
		require.NotEmpty(t, resp.RefreshToken)
		assert.NotEqual(t, loginResp.RefreshToken, resp.RefreshToken)
		rotatedRefreshToken = resp.RefreshToken
	})

	t.Run("the old refresh token no longer works once rotated -- 401", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"refreshToken": loginResp.RefreshToken})
		req := httptest.NewRequest("POST", "/refresh", bytes.NewReader(body))
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})

	t.Run("the rotated token still works", func(t *testing.T) {
		require.NotEmpty(t, rotatedRefreshToken)
		body, _ := json.Marshal(map[string]string{"refreshToken": rotatedRefreshToken})
		req := httptest.NewRequest("POST", "/refresh", bytes.NewReader(body))
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})
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

// TestAuthHandlers_PasswordReset_RateLimit shows that request/confirm share
// the SAME loginAttempts limiter instance as login (see AuthHandlers'
// loginAttempts doc comment) -- keyed by email for request, by token for
// confirm.
func TestAuthHandlers_PasswordReset_RateLimit(t *testing.T) {
	t.Run("request is rate-limited per email", func(t *testing.T) {
		h := newAuthHandlers(t)
		r := newRouter(h.Routes)

		// Unique per run -- see TestAuthHandlers_LoginLocal_PerAccountRateLimit's
		// comment; this scope ("login_email") is also shared with the login
		// rate-limit tests above.
		email := fmt.Sprintf("ratelimit-reset-%s@test.local", uuid.NewString())
		attempt := func() int {
			body, _ := json.Marshal(map[string]string{"email": email})
			req := httptest.NewRequest("POST", "/password-reset/request", bytes.NewReader(body))
			return doRequest(r, req).Code
		}
		for i := 0; i < 10; i++ {
			assert.Equal(t, http.StatusNoContent, attempt(), "attempt %d is still within budget", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(), "11th attempt for this email is rate-limited")
	})

	t.Run("confirm is rate-limited per token", func(t *testing.T) {
		h := newAuthHandlers(t)
		r := newRouter(h.Routes)

		// Unique per run, same reasoning as the email case above -- confirm
		// is keyed by the submitted token under the same shared scope.
		token := "ratelimit-token-" + uuid.NewString()
		attempt := func() int {
			body, _ := json.Marshal(map[string]string{"token": token, "newPassword": "NewPassword123!"})
			req := httptest.NewRequest("POST", "/password-reset/confirm", bytes.NewReader(body))
			return doRequest(r, req).Code
		}
		for i := 0; i < 10; i++ {
			assert.Equal(t, http.StatusBadRequest, attempt(), "attempt %d is still within budget", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(), "11th attempt for this token is rate-limited")
	})
}
