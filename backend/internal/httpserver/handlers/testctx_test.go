package handlers_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/httpserver/middleware"
)

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
