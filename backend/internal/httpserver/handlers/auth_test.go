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
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/sessioncookie"
	"github.com/kuruops/kuruops/internal/testutil"
)

// newAuthHandlers wires AuthHandlers against the real seeded default tenant
// (admin@kuruops.local / ChangeMe123!, see db/migrations/0002_seed_default_admin.up.sql)
// -- resolveTenant always resolves that single tenant, so login-success
// tests have no other way to reach it than through the actual seed data.
func newAuthHandlers(t *testing.T) *handlers.AuthHandlers {
	t.Helper()
	h, _ := newAuthHandlersAndService(t)
	return h
}

// newAuthHandlersAndService also hands back the underlying AuthService --
// most tests in this file only need the HTTP surface, but the MFA tests
// need to enroll/confirm a secret directly (there's no HTTP route for
// AccountHandlers here, only AuthHandlers' login/verify routes) before
// exercising the login flow through HTTP.
func newAuthHandlersAndService(t *testing.T) (*handlers.AuthHandlers, *service.AuthService) {
	t.Helper()
	pool := testutil.RequireTestDB(t)

	// loginAttempts (scope "login_email") is backed by a shared Postgres
	// table now, not a fresh in-memory map per test process (see
	// KeyedLimiter) -- several tests in this file legitimately submit the
	// real seeded admin@kuruops.local address to exercise a successful
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
	roleSvc := service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository())
	store := secrets.NewEnvStore()
	authSvc := service.NewAuthService(pool, tenants, users, repository.NewRefreshTokenRepository(), repository.NewMFAPendingTokenRepository(), roleSvc, issuer, store)
	identityCfg := repository.NewIdentityConfigRepository()
	ldapSvc := service.NewLDAPAuthService(pool, identityCfg, store, authSvc)
	samlSvc := service.NewSAMLAuthService(pool, identityCfg, store, authSvc, "https://kuruops.test")
	// noopSender is defined in smtp_config_test.go (same package) -- these
	// login/refresh/SAML tests never actually exercise password-reset email
	// delivery, so a real Sender isn't needed here.
	smtpSvc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), store, noopSender{}, repository.NewAdminAuditEventRepository())
	passwordResetSvc := service.NewPasswordResetService(pool, repository.NewPasswordResetRepository(), users, repository.NewRefreshTokenRepository(), smtpSvc, "http://localhost:3000")

	return handlers.NewAuthHandlers(t.Context(), pool.Pool, authSvc, ldapSvc, samlSvc, passwordResetSvc, "https://kuruops.test"), authSvc
}

func TestAuthHandlers_LoginLocal(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	t.Run("correct seeded credentials -- 200, mustChangePassword true", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"email": "admin@kuruops.local", "password": "ChangeMe123!"})
		req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["token"])
		user := resp["user"].(map[string]any)
		assert.Equal(t, "admin@kuruops.local", user["email"])
	})

	t.Run("wrong password -- 401", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"email": "admin@kuruops.local", "password": "wrong"})
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

// refreshCookie pulls the refresh token out of a response's Set-Cookie
// header. Every assertion about the cookie's own attributes lives in
// TestRefreshCookieAttributes; this one just needs the value.
func refreshCookie(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessioncookie.Name {
			return c.Value
		}
	}
	t.Fatalf("no %s cookie in the response", sessioncookie.Name)
	return ""
}

// withRefreshCookie presents a refresh token the way a browser would.
func withRefreshCookie(req *http.Request, token string) *http.Request {
	req.AddCookie(&http.Cookie{Name: sessioncookie.Name, Value: token})
	return req
}

// TestAuthHandlers_Refresh exercises the one handler in this file with 0%
// coverage before this test existed: a real login (against the seeded
// default admin) issues a refresh token, which is then exchanged, rejected
// when missing/unknown, and confirmed to actually rotate (the old token
// stops working once a new one has been issued from it).
//
// The token now travels as an HttpOnly cookie rather than a JSON field, so
// every step here presents it the way a browser would -- see package
// sessioncookie.
func TestAuthHandlers_Refresh(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	loginBody, _ := json.Marshal(map[string]string{"email": "admin@kuruops.local", "password": "ChangeMe123!"})
	loginReq := httptest.NewRequest("POST", "/login", bytes.NewReader(loginBody))
	loginRec := doRequest(r, loginReq)
	require.Equal(t, http.StatusOK, loginRec.Code)
	originalToken := refreshCookie(t, loginRec)
	require.NotEmpty(t, originalToken)

	t.Run("the login response body carries no refresh token", func(t *testing.T) {
		// The whole point of the HttpOnly cookie: script on the page must
		// have no way to read this value. A body field would hand it back.
		var body map[string]any
		require.NoError(t, json.Unmarshal(loginRec.Body.Bytes(), &body))
		assert.NotContains(t, body, "refreshToken")
		assert.NotEmpty(t, body["token"], "the short-lived access token still comes back in the body")
	})

	t.Run("no cookie -- 401", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/refresh", nil)
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})

	t.Run("a refresh token in the body is ignored -- 401", func(t *testing.T) {
		// Accepting a body fallback would undo the migration: an attacker
		// who scraped a token from somewhere could still replay it.
		body, _ := json.Marshal(map[string]string{"refreshToken": originalToken})
		req := httptest.NewRequest("POST", "/refresh", bytes.NewReader(body))
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})

	t.Run("unknown token -- 401, and the stale cookie is cleared", func(t *testing.T) {
		req := withRefreshCookie(httptest.NewRequest("POST", "/refresh", nil), "rt_not-a-real-token")
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		// Leaving a known-dead cookie in place would 401 every future
		// refresh until it aged out on its own.
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessioncookie.Name {
				assert.Empty(t, c.Value)
				assert.Negative(t, c.MaxAge)
				return
			}
		}
		t.Fatal("expected the stale refresh cookie to be cleared")
	})

	var rotated string
	t.Run("valid token -- 200, rotates the cookie", func(t *testing.T) {
		req := withRefreshCookie(httptest.NewRequest("POST", "/refresh", nil), originalToken)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["token"])
		assert.NotContains(t, resp, "refreshToken", "the rotated token goes back as a cookie, not a body field")

		rotated = refreshCookie(t, rec)
		require.NotEmpty(t, rotated)
		assert.NotEqual(t, originalToken, rotated)
	})

	t.Run("the old refresh token no longer works once rotated -- 401", func(t *testing.T) {
		req := withRefreshCookie(httptest.NewRequest("POST", "/refresh", nil), originalToken)
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})

	t.Run("the rotated token still works", func(t *testing.T) {
		require.NotEmpty(t, rotated)
		req := withRefreshCookie(httptest.NewRequest("POST", "/refresh", nil), rotated)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})
}

// TestRefreshCookieAttributes pins the four attributes the whole migration
// rests on. Each one is load-bearing and silently weakenable: drop HttpOnly
// and script can read the token again, drop SameSite and a cross-site
// request can rotate or revoke a session, widen Path and a 30-day
// credential rides along on every API call.
func TestRefreshCookieAttributes(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"email": "admin@kuruops.local", "password": "ChangeMe123!"})
	rec := doRequest(r, httptest.NewRequest("POST", "/login", bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code)

	var c *http.Cookie
	for _, got := range rec.Result().Cookies() {
		if got.Name == sessioncookie.Name {
			c = got
		}
	}
	require.NotNil(t, c)

	assert.True(t, c.HttpOnly, "script on the page must not be able to read the refresh token")
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.Equal(t, "/auth", c.Path, "only /auth/refresh and /auth/logout ever need this cookie")
	assert.Equal(t, int(sessioncookie.RefreshTTL.Seconds()), c.MaxAge,
		"the cookie must expire exactly when the token it carries does")
	// newAuthHandlers builds these with an https APP_BASE_URL.
	assert.True(t, c.Secure, "Secure follows APP_BASE_URL's scheme -- see sessioncookie.Secure")
}

func TestAuthHandlers_Logout(t *testing.T) {
	h := newAuthHandlers(t)
	r := newRouter(h.Routes)

	login := func(t *testing.T) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"email": "admin@kuruops.local", "password": "ChangeMe123!"})
		rec := doRequest(r, httptest.NewRequest("POST", "/login", bytes.NewReader(body)))
		require.Equal(t, http.StatusOK, rec.Code)
		return refreshCookie(t, rec)
	}

	t.Run("no cookie -- still 204, not an error", func(t *testing.T) {
		// logout deliberately never fails on a missing token -- see
		// AuthHandlers.logout's doc comment.
		req := httptest.NewRequest("POST", "/logout", nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})

	t.Run("unknown token -- 204", func(t *testing.T) {
		req := withRefreshCookie(httptest.NewRequest("POST", "/logout", nil), "rt_not-a-real-token")
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})

	t.Run("the cookie is cleared even when there was nothing to revoke", func(t *testing.T) {
		// Getting the browser out of a logged-in state must not depend on
		// the server-side revoke finding anything.
		rec := doRequest(r, httptest.NewRequest("POST", "/logout", nil))
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessioncookie.Name {
				assert.Empty(t, c.Value)
				assert.Negative(t, c.MaxAge)
				assert.Equal(t, "/auth", c.Path, "clearing from a different Path would leave the original in place")
				return
			}
		}
		t.Fatal("expected logout to clear the refresh cookie")
	})

	t.Run("a valid token -- 204, and it no longer works afterward", func(t *testing.T) {
		token := login(t)

		req := withRefreshCookie(httptest.NewRequest("POST", "/logout", nil), token)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		refreshReq := withRefreshCookie(httptest.NewRequest("POST", "/refresh", nil), token)
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, refreshReq).Code)
	})

	t.Run("logging out one session does not affect another session for the same user", func(t *testing.T) {
		tokenA := login(t)
		tokenB := login(t)

		req := withRefreshCookie(httptest.NewRequest("POST", "/logout", nil), tokenA)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		refreshReq := withRefreshCookie(httptest.NewRequest("POST", "/refresh", nil), tokenB)
		assert.Equal(t, http.StatusOK, doRequest(r, refreshReq).Code)
	})
}

// TestAuthHandlers_LoginLocal_MFA exercises the full two-step login for a
// TOTP-enrolled account through HTTP: /login returns a pending token
// instead of a session, and /mfa/verify (or a wrong code/token) decides
// what happens next -- same fixture-in-the-real-default-tenant shape as
// TestAuthHandlers_LoginLocal, since resolveTenant always resolves that one
// tenant, not an arbitrary testutil.NewTenant().
func TestAuthHandlers_LoginLocal_MFA(t *testing.T) {
	h, authSvc := newAuthHandlersAndService(t)
	r := newRouter(h.Routes)

	tenant, err := authSvc.ResolveDefaultTenant(t.Context())
	require.NoError(t, err)
	userID := testutil.NewUser(t, tenant.ID, "analyst", nil)
	pool := testutil.RequireTestDB(t)
	tx := testutil.BeginTx(t, pool, tenant.ID)
	user, err := repository.NewUserRepository().Get(t.Context(), tx, tenant.ID, userID)
	require.NoError(t, err)
	email := user.Email

	secret, _, err := authSvc.GenerateMFAEnrollment(t.Context(), tenant.ID, userID)
	require.NoError(t, err)
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	_, err = authSvc.ConfirmMFA(t.Context(), tenant.ID, userID, secret, code)
	require.NoError(t, err)

	var pendingToken string
	t.Run("login with a correct password returns mfaRequired + a pending token, not a session", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"email": email, "password": testutil.TestPassword})
		req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			MFARequired  bool   `json:"mfaRequired"`
			PendingToken string `json:"pendingToken"`
			Token        string `json:"token"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.True(t, resp.MFARequired)
		assert.NotEmpty(t, resp.PendingToken)
		assert.Empty(t, resp.Token, "no session token before the code is verified")
		pendingToken = resp.PendingToken
	})

	t.Run("verify with a wrong code -- 401, pending token still usable afterward", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"pendingToken": pendingToken, "code": "000000"})
		req := httptest.NewRequest("POST", "/mfa/verify", bytes.NewReader(body))
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})

	t.Run("verify with an unknown pending token -- 401", func(t *testing.T) {
		freshCode, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)
		body, _ := json.Marshal(map[string]string{"pendingToken": "mfap_no-such-token", "code": freshCode})
		req := httptest.NewRequest("POST", "/mfa/verify", bytes.NewReader(body))
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})

	t.Run("malformed body -- 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/mfa/verify", bytes.NewReader([]byte("not json")))
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("verify with the correct code -- 200 with a real session", func(t *testing.T) {
		freshCode, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)
		body, _ := json.Marshal(map[string]string{"pendingToken": pendingToken, "code": freshCode})
		req := httptest.NewRequest("POST", "/mfa/verify", bytes.NewReader(body))
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["token"])
		assert.NotContains(t, resp, "refreshToken", "MFA's second step sets the cookie, same as a plain login")
		assert.NotEmpty(t, refreshCookie(t, rec))

		t.Run("the same pending token cannot be reused afterward", func(t *testing.T) {
			freshCode, err := totp.GenerateCode(secret, time.Now())
			require.NoError(t, err)
			body, _ := json.Marshal(map[string]string{"pendingToken": pendingToken, "code": freshCode})
			req := httptest.NewRequest("POST", "/mfa/verify", bytes.NewReader(body))
			assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
		})
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
