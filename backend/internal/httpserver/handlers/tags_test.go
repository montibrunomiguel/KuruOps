package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newTagHandlerFixture(t *testing.T) (h *handlers.TagHandlers, tenantID, actorID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	h = handlers.NewTagHandlers(service.NewTagService(pool, repository.NewTagRepository()))
	return h, tenantID, actorID
}

func TestTagHandlers_ListRoute(t *testing.T) {
	h, tenantID, _ := newTagHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	var tags []domain.Tag
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tags))
	assert.Empty(t, tags)
}

func TestTagHandlers_SettingsRoutes(t *testing.T) {
	h, tenantID, actorID := newTagHandlerFixture(t)
	r := newRouter(h.SettingsRoutes)

	t.Run("empty name -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": ""})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	body, _ := json.Marshal(map[string]string{"name": "phishing"})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var tag domain.Tag
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tag))

	req = withClaims(httptest.NewRequest("DELETE", "/"+tag.ID.String(), nil), tenantID, actorID, nil)
	rec = doRequest(r, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
