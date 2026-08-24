package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newAdminAuditLogHandlerFixture(t *testing.T) (h *handlers.AdminAuditLogHandlers, tenantID, actorID uuid.UUID, tagSvc *service.TagService) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	userSvc := service.NewUserService(pool, repository.NewUserRepository(), auditRepo)
	tagSvc = service.NewTagService(pool, repository.NewTagRepository(), auditRepo)
	svc := service.NewAdminAuditLogService(pool, auditRepo, userSvc)
	return handlers.NewAdminAuditLogHandlers(svc), tenantID, actorID, tagSvc
}

func TestAdminAuditLogHandlers_List(t *testing.T) {
	h, tenantID, actorID, tagSvc := newAdminAuditLogHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("empty log -- 200 with an empty events array", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			Events     []map[string]any `json:"events"`
			NextCursor *struct{}        `json:"nextCursor"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Empty(t, body.Events)
		assert.Nil(t, body.NextCursor)
	})

	_, err := tagSvc.Create(t.Context(), tenantID, actorID, "phishing", nil)
	require.NoError(t, err)

	t.Run("reflects a real committed audit event with the actor's name", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			Events []struct {
				Area      string `json:"area"`
				Action    string `json:"action"`
				ActorName string `json:"actorName"`
			} `json:"events"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body.Events, 1)
		assert.Equal(t, "tags", body.Events[0].Area)
		assert.Equal(t, "create", body.Events[0].Action)
		assert.Equal(t, "Test User", body.Events[0].ActorName)
	})

	t.Run("invalid beforeCreatedAt -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?beforeCreatedAt=not-a-time&beforeId=1", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("beforeCreatedAt without beforeId -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?beforeCreatedAt=2026-01-01T00:00:00Z", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestAdminAuditLogHandlers_MissingTenantContext(t *testing.T) {
	h := handlers.NewAdminAuditLogHandlers(nil)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
