package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/kuruops/kuruops/internal/httpserver/middleware"
)

var routeParamPattern = regexp.MustCompile(`\{[^}]+\}`)

// assertAllRoutesRequireTenant walks every route a handler's Routes method
// registers and confirms each one 401s when the request carries no tenant
// claims at all -- the mustTenantID(w, r) guard every handler method calls
// first (see respond.go), before it does anything else. URL params are
// substituted with a syntactically-valid placeholder UUID purely so the
// route matches; mustTenantID short-circuits before any of them are parsed,
// so the placeholder's actual value never matters.
func assertAllRoutesRequireTenant(t *testing.T, mount func(r chi.Router)) {
	t.Helper()
	r := newRouter(mount)
	routes, ok := r.(chi.Routes)
	if !ok {
		t.Fatal("router does not satisfy chi.Routes")
	}

	err := chi.Walk(routes, func(method, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		path := routeParamPattern.ReplaceAllString(route, "00000000-0000-0000-0000-000000000001")
		req := httptest.NewRequest(method, path, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s must 401 without tenant claims", method, route)
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
}

// withClaims stands in for JWTAuth/DevHeaderAuth in these tests -- handler
// logic reads tenant/user/allowedTags out of the request context via
// middleware accessors, so tests exercise that same contract directly rather
// than round-tripping through a real JWT (already covered by
// internal/httpserver/middleware/auth_test.go).
func withClaims(r *http.Request, tenantID, userID uuid.UUID, allowedTags []string) *http.Request {
	return r.WithContext(middleware.WithClaims(r.Context(), middleware.Claims{
		TenantID: tenantID, UserID: userID, AllowedTags: allowedTags,
	}))
}

// newRouter mounts routes onto a fresh chi.Router at "/" so URL params
// (chi.URLParam) resolve the same way they do under the real router.go.
func newRouter(mount func(r chi.Router)) chi.Router {
	r := chi.NewRouter()
	mount(r)
	return r
}

func doRequest(r chi.Router, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
