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

	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newWebhookHandlerFixture(t *testing.T) (h *handlers.WebhookHandlers, tenantID, actorID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	h = handlers.NewWebhookHandlers(service.NewWebhookService(pool, repository.NewWebhookRepository()))
	return h, tenantID, actorID
}

func TestWebhookHandlers_CreateListRegenerateEnableDisable(t *testing.T) {
	h, tenantID, actorID := newWebhookHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("create missing fields -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": ""})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]string{"name": "Wazuh Prod", "source": "wazuh"})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created["token"], "the plaintext token is returned exactly once, on create")
	endpoint := created["endpoint"].(map[string]any)
	id := endpoint["id"].(string)

	t.Run("list", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("regenerate", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+id+"/regenerate", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("disable then enable", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+id+"/disable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		req = withClaims(httptest.NewRequest("POST", "/"+id+"/enable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}
