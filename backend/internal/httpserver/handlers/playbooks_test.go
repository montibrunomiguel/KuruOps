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

func newPlaybookHandlerFixture(t *testing.T) (h *handlers.PlaybookHandlers, tenantID, actorID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	h = handlers.NewPlaybookHandlers(service.NewPlaybookService(pool, repository.NewPlaybookRepository()))
	return h, tenantID, actorID
}

func TestPlaybookHandlers_CreateGetUpdateDelete(t *testing.T) {
	h, tenantID, actorID := newPlaybookHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("create missing title -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"category": "Phishing"})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]any{"title": "Phishing Response", "category": "Phishing"})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var pb domain.Playbook
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pb))

	t.Run("get", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+pb.ID.String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("get unknown -- 404", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+uuid.New().String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNotFound, doRequest(r, req).Code)
	})

	t.Run("update", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"title": "Phishing Response v2", "category": "Phishing"})
		req := withClaims(httptest.NewRequest("PUT", "/"+pb.ID.String(), bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("delete", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/"+pb.ID.String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}

func TestPlaybookHandlers_Match(t *testing.T) {
	h, tenantID, actorID := newPlaybookHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("missing title query param -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/match", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("no match -- 200 with null body", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/match?title=Anything", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "null\n", rec.Body.String())
	})
}

func TestPlaybookHandlers_List(t *testing.T) {
	h, tenantID, actorID := newPlaybookHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("empty list -- 200", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "[]\n", rec.Body.String())
	})

	body, _ := json.Marshal(map[string]any{"title": "Phishing Response", "category": "Phishing"})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	require.Equal(t, http.StatusCreated, doRequest(r, req).Code)

	t.Run("list reflects created playbook", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var pbs []domain.Playbook
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pbs))
		require.Len(t, pbs, 1)
	})
}

func TestPlaybookHandlers_ValidationErrors(t *testing.T) {
	h, tenantID, actorID := newPlaybookHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("get invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/not-a-uuid", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("create invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("update invalid id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"title": "X"})
		req := withClaims(httptest.NewRequest("PUT", "/not-a-uuid", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("update invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+uuid.New().String(), bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("delete invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/not-a-uuid", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestPlaybookHandlers_List_MissingTenantContext(t *testing.T) {
	h := handlers.NewPlaybookHandlers(nil)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
