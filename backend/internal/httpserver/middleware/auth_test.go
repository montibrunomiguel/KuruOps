package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/httpserver/middleware"
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
		token, err := issuer.Issue(tenantID, userID, true, []string{"alerts", "followup"}, []string{"CompanyA"}, true)
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
		middleware.JWTAuth(verifier)(next).ServeHTTP(rec, req)

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
		middleware.JWTAuth(verifier)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("header without the Bearer prefix is rejected", func(t *testing.T) {
		token, err := issuer.Issue(tenantID, userID, true, nil, nil, false)
		require.NoError(t, err)
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", token) // no "Bearer " prefix
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("empty bearer token is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer ")
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("invalid/tampered token is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer not-a-real-token")
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("token signed by an unrelated key is rejected", func(t *testing.T) {
		otherPriv, err := authn.GenerateEphemeralKeyPair()
		require.NoError(t, err)
		otherIssuer := authn.NewIssuer(otherPriv)
		token, err := otherIssuer.Issue(tenantID, userID, true, nil, nil, false)
		require.NoError(t, err)

		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		middleware.JWTAuth(verifier)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

// fakePATResolver stands in for service.PersonalAccessTokenService --
// APIAuth only depends on the PATResolver interface, so this avoids
// needing a real database for these tests.
type fakePATResolver struct {
	tenantID, userID            uuid.UUID
	isAdmin                     bool
	resourceAccess, allowedTags []string
	found                       bool
	err                         error
	gotToken                    string
}

func (f *fakePATResolver) Resolve(_ context.Context, token string) (uuid.UUID, uuid.UUID, bool, []string, []string, bool, error) {
	f.gotToken = token
	return f.tenantID, f.userID, f.isAdmin, f.resourceAccess, f.allowedTags, f.found, f.err
}

func TestAPIAuth(t *testing.T) {
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	issuer := authn.NewIssuer(priv)
	verifier := authn.NewVerifier(&priv.PublicKey)
	tenantID, userID := uuid.New(), uuid.New()

	t.Run("a session JWT still authenticates, same as JWTAuth", func(t *testing.T) {
		token, err := issuer.Issue(tenantID, userID, true, []string{"alerts"}, nil, false)
		require.NoError(t, err)

		var gotTenant uuid.UUID
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotTenant, _ = middleware.TenantID(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		middleware.APIAuth(verifier, &fakePATResolver{})(next).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, tenantID, gotTenant)
	})

	t.Run("a pat_-prefixed token resolves through patResolver instead of the JWT verifier", func(t *testing.T) {
		resolver := &fakePATResolver{
			tenantID: tenantID, userID: userID, isAdmin: true,
			resourceAccess: []string{"incidents"}, allowedTags: []string{"CompanyA"}, found: true,
		}

		var gotClaims middleware.Claims
		var gotOK bool
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotTenant, _ := middleware.TenantID(r.Context())
			gotUser, _ := middleware.UserID(r.Context())
			gotIsAdmin, _ := middleware.IsAdmin(r.Context())
			gotAccess, _ := middleware.ResourceAccess(r.Context())
			gotTags := middleware.AllowedTags(r.Context())
			gotMustChange, ok := middleware.MustChangePassword(r.Context())
			gotClaims = middleware.Claims{
				TenantID: gotTenant, UserID: gotUser, IsAdmin: gotIsAdmin,
				ResourceAccess: gotAccess, AllowedTags: gotTags, MustChangePassword: gotMustChange,
			}
			gotOK = ok
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer pat_abc123")
		rec := httptest.NewRecorder()
		middleware.APIAuth(verifier, resolver)(next).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "pat_abc123", resolver.gotToken)
		assert.Equal(t, tenantID, gotClaims.TenantID)
		assert.Equal(t, userID, gotClaims.UserID)
		assert.True(t, gotClaims.IsAdmin)
		assert.Equal(t, []string{"incidents"}, gotClaims.ResourceAccess)
		assert.Equal(t, []string{"CompanyA"}, gotClaims.AllowedTags)
		assert.True(t, gotOK)
		assert.False(t, gotClaims.MustChangePassword, "a PAT holder is never mid-forced-password-change")
	})

	t.Run("an unknown/expired/revoked pat_ token is rejected without falling back to JWT verification", func(t *testing.T) {
		resolver := &fakePATResolver{found: false}
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer pat_no-such-token")
		rec := httptest.NewRecorder()
		middleware.APIAuth(verifier, resolver)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("patResolver returning an error is a 500, not a 401", func(t *testing.T) {
		resolver := &fakePATResolver{err: errors.New("db unreachable")}
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer pat_whatever")
		rec := httptest.NewRecorder()
		middleware.APIAuth(verifier, resolver)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("a nil patResolver falls through to JWT verification even for a pat_-prefixed token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		req.Header.Set("Authorization", "Bearer pat_whatever")
		rec := httptest.NewRecorder()
		middleware.APIAuth(verifier, nil)(okHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("missing Authorization header is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/alerts", nil)
		rec := httptest.NewRecorder()
		middleware.APIAuth(verifier, &fakePATResolver{})(okHandler()).ServeHTTP(rec, req)
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
