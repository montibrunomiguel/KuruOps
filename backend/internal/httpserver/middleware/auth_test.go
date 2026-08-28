package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestJWTAuth(t *testing.T) {
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	issuer := authn.NewIssuer(priv)
	verifier := authn.NewVerifier(&priv.PublicKey)
	tenantID, userID := uuid.New(), uuid.New()

	t.Run("valid bearer token propagates every claim into the request context", func(t *testing.T) {
		token, err := issuer.Issue(tenantID, userID, true, []string{"alerts", "followup"}, []string{"CompanyA"}, true, false)
		require.NoError(t, err)

		var gotTenant uuid.UUID
		var gotUser uuid.UUID
		var gotIsAdmin bool
		var gotAccess []string
		var gotTags []string
		var gotMustChange, gotOK bool

		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotTenant, _ = middleware.TenantID(r.Context())
			gotUser, _ = middleware.UserID(r.Context())
			gotIsAdmin, _ = middleware.IsAdmin(r.Context())
			gotAccess, _ = middleware.ResourceAccess(r.Context())
			gotTags = middleware.AllowedTags(r.Context())
			gotMustChange, gotOK = middleware.MustChangePassword(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier, nil)(next).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, tenantID, gotTenant)
		assert.Equal(t, userID, gotUser)
		assert.True(t, gotIsAdmin)
		assert.Equal(t, []string{"alerts", "followup"}, gotAccess)
		assert.Equal(t, []string{"CompanyA"}, gotTags)
		assert.True(t, gotMustChange)
		assert.True(t, gotOK)
	})

	t.Run("missing Authorization header is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier, nil)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("header without the Bearer prefix is rejected", func(t *testing.T) {
		token, err := issuer.Issue(tenantID, userID, true, nil, nil, false, false)
		require.NoError(t, err)
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", token) // no "Bearer " prefix
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier, nil)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("empty bearer token is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer ")
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier, nil)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("invalid/tampered token is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer not-a-real-token")
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier, nil)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("token signed by an unrelated key is rejected", func(t *testing.T) {
		otherPriv, err := authn.GenerateEphemeralKeyPair()
		require.NoError(t, err)
		otherIssuer := authn.NewIssuer(otherPriv)
		token, err := otherIssuer.Issue(tenantID, userID, true, nil, nil, false, false)
		require.NoError(t, err)

		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier, nil)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

// TestJWTAuth_PersonalAPIToken is the end-to-end regression test for
// personal API tokens: a pat_-prefixed bearer token authenticates through
// the SAME middleware as a JWT, without ever reaching verifier.Verify (a
// pat_ token isn't a JWT at all). Claims come from the token owner's
// CURRENT Role, resolved fresh on every request -- so a role change takes
// effect on an already-issued token immediately, and revoking it rejects
// the very next request.
func TestJWTAuth_PersonalAPIToken(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", []string{"alerts"})
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	verifier := authn.NewVerifier(&priv.PublicKey)
	apiTokens := service.NewUserAPITokenService(pool, repository.NewUserAPITokenRepository(), repository.NewUserRepository())

	result, err := apiTokens.Create(t.Context(), tenantID, userID, "CI script", nil)
	require.NoError(t, err)

	doRequest := func(token string) (*httptest.ResponseRecorder, []string) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		var gotAccess []string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAccess, _ = middleware.ResourceAccess(r.Context())
			w.WriteHeader(http.StatusOK)
		})
		middleware.JWTAuth(verifier, apiTokens)(next).ServeHTTP(rec, req)
		return rec, gotAccess
	}

	t.Run("a valid personal API token authenticates with the owner's current resource access", func(t *testing.T) {
		rec, gotAccess := doRequest(result.Plaintext)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []string{"alerts"}, gotAccess)
	})

	t.Run("garbage pat_-prefixed token is rejected without touching the JWT verifier", func(t *testing.T) {
		rec, _ := doRequest("pat_this-does-not-exist")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("a revoked token is rejected on the very next request", func(t *testing.T) {
		require.NoError(t, apiTokens.Revoke(t.Context(), tenantID, userID, result.Token.ID))
		rec, _ := doRequest(result.Plaintext)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestDevHeaderAuth(t *testing.T) {
	tenantID, userID := uuid.New(), uuid.New()

	t.Run("full headers propagate exactly", func(t *testing.T) {
		var gotIsAdmin bool
		var gotAccess []string
		var gotTags []string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotIsAdmin, _ = middleware.IsAdmin(r.Context())
			gotAccess, _ = middleware.ResourceAccess(r.Context())
			gotTags = middleware.AllowedTags(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("X-Tenant-ID", tenantID.String())
		req.Header.Set("X-User-ID", userID.String())
		req.Header.Set("X-Is-Admin", "true")
		req.Header.Set("X-Resource-Access", "alerts,followup")
		req.Header.Set("X-Allowed-Tags", "CompanyA,CompanyB")
		rec := httptest.NewRecorder()
		middleware.DevHeaderAuth(next).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, gotIsAdmin)
		assert.Equal(t, []string{"alerts", "followup"}, gotAccess)
		assert.Equal(t, []string{"CompanyA", "CompanyB"}, gotTags)
	})

	t.Run("defaults apply when optional headers are omitted", func(t *testing.T) {
		var gotIsAdmin bool
		var gotAccess []string
		var gotTags []string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotIsAdmin, _ = middleware.IsAdmin(r.Context())
			gotAccess, _ = middleware.ResourceAccess(r.Context())
			gotTags = middleware.AllowedTags(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("X-Tenant-ID", tenantID.String())
		req.Header.Set("X-User-ID", userID.String())
		rec := httptest.NewRecorder()
		middleware.DevHeaderAuth(next).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.False(t, gotIsAdmin)
		assert.Equal(t, []string{"alerts", "incidents"}, gotAccess)
		assert.Empty(t, gotTags)
	})

	t.Run("missing X-Tenant-ID is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("X-User-ID", userID.String())
		rec := httptest.NewRecorder()
		middleware.DevHeaderAuth(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("invalid X-Tenant-ID is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("X-Tenant-ID", "not-a-uuid")
		req.Header.Set("X-User-ID", userID.String())
		rec := httptest.NewRecorder()
		middleware.DevHeaderAuth(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("missing X-User-ID is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("X-Tenant-ID", tenantID.String())
		rec := httptest.NewRecorder()
		middleware.DevHeaderAuth(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestRequireAdmin(t *testing.T) {
	t.Run("admin passes through", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), middleware.Claims{IsAdmin: true}))
		rec := httptest.NewRecorder()
		middleware.RequireAdmin()(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("non-admin is forbidden", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), middleware.Claims{IsAdmin: false}))
		rec := httptest.NewRecorder()
		middleware.RequireAdmin()(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("missing auth context is unauthorized, not forbidden", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		middleware.RequireAdmin()(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestRequireResourceAccess(t *testing.T) {
	t.Run("capability present in the set passes through", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), middleware.Claims{ResourceAccess: []string{"alerts", "followup"}}))
		rec := httptest.NewRecorder()
		middleware.RequireResourceAccess("followup")(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("capability absent from the set is forbidden", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), middleware.Claims{ResourceAccess: []string{"alerts"}}))
		rec := httptest.NewRecorder()
		middleware.RequireResourceAccess("incidents")(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("empty resourceAccess set is forbidden for any capability", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), middleware.Claims{ResourceAccess: []string{}}))
		rec := httptest.NewRecorder()
		middleware.RequireResourceAccess("alerts")(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("missing auth context is unauthorized", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		middleware.RequireResourceAccess("alerts")(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestRequirePasswordChanged(t *testing.T) {
	const changePasswordPath = "/api/v1/account/change-password"

	t.Run("blocks every path except the exact change-password path when must-change is set", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), middleware.Claims{MustChangePassword: true}))
		rec := httptest.NewRecorder()
		middleware.RequirePasswordChanged(changePasswordPath)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("lets the exact change-password path through when must-change is set", func(t *testing.T) {
		req := httptest.NewRequest("POST", changePasswordPath, nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), middleware.Claims{MustChangePassword: true}))
		rec := httptest.NewRecorder()
		middleware.RequirePasswordChanged(changePasswordPath)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("a path that merely starts with the change-password path is still blocked", func(t *testing.T) {
		req := httptest.NewRequest("POST", changePasswordPath+"/extra", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), middleware.Claims{MustChangePassword: true}))
		rec := httptest.NewRecorder()
		middleware.RequirePasswordChanged(changePasswordPath)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, "the exemption must be an exact path match, not a prefix match")
	})

	t.Run("passes through freely when must-change is not set", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), middleware.Claims{MustChangePassword: false}))
		rec := httptest.NewRecorder()
		middleware.RequirePasswordChanged(changePasswordPath)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("passes through when there is no auth context at all", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		rec := httptest.NewRecorder()
		middleware.RequirePasswordChanged(changePasswordPath)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestContextAccessors_MissingValues(t *testing.T) {
	ctx := httptest.NewRequest("GET", "/", nil).Context()

	_, ok := middleware.TenantID(ctx)
	assert.False(t, ok)

	_, ok = middleware.UserID(ctx)
	assert.False(t, ok)

	_, ok = middleware.IsAdmin(ctx)
	assert.False(t, ok)

	_, ok = middleware.ResourceAccess(ctx)
	assert.False(t, ok)

	assert.Empty(t, middleware.AllowedTags(ctx))

	_, ok = middleware.MustChangePassword(ctx)
	assert.False(t, ok)
}
