package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestSlackConfigHandlers_GetDisconnect(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewSlackConfigService(pool, repository.NewSlackConfigRepository(), secrets.NewEnvStore(), nil, "", "", "")
	h := handlers.NewSlackConfigHandlers(svc)
	r := newRouter(h.Routes)

	t.Run("get before any workspace connected -- 200 with null body", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "null\n", rec.Body.String())
	})

	t.Run("disconnect when nothing is connected -- still 204, not an error", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}

func TestSlackConfigHandlers_AuthorizeURL(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	// oauth_states.user_id is a real FK to users(id) (the installing
	// admin's identity is meaningful, not a throwaway) -- same reasoning as
	// TestStorageConfigHandlers_GDriveAuthorizeURL.
	userID := testutil.NewUser(t, tenantID, "admin", nil)

	t.Run("no Slack client configured -- 400", func(t *testing.T) {
		svc := service.NewSlackConfigService(pool, repository.NewSlackConfigRepository(), secrets.NewEnvStore(), nil, "", "", "")
		h := handlers.NewSlackConfigHandlers(svc)
		r := newRouter(h.Routes)

		req := withClaims(httptest.NewRequest("GET", "/oauth/authorize-url", nil), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("configured client returns a redirect url", func(t *testing.T) {
		oauthStates := service.NewOAuthStateService(pool, repository.NewOAuthStateRepository())
		svc := service.NewSlackConfigService(pool, repository.NewSlackConfigRepository(), secrets.NewEnvStore(),
			oauthStates, "test-client-id", "test-client-secret", "https://argusops.example/auth/oauth/slack/callback")
		h := handlers.NewSlackConfigHandlers(svc)
		r := newRouter(h.Routes)

		req := withClaims(httptest.NewRequest("GET", "/oauth/authorize-url", nil), tenantID, userID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "slack.com/oauth/v2/authorize")
	})
}

func TestSlackConfigHandlers_MissingTenantContext(t *testing.T) {
	h := handlers.NewSlackConfigHandlers(nil)
	r := newRouter(h.Routes)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/"}, {"DELETE", "/"}, {"GET", "/oauth/authorize-url"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
		})
	}
}
