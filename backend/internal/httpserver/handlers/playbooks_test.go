package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	h = handlers.NewPlaybookHandlers(service.NewPlaybookService(pool, repository.NewPlaybookRepository(), repository.NewAlertRepository(), "https://argusops.example"))
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

	body, _ := json.Marshal(map[string]any{
		"title": "Phishing Response", "category": "Phishing",
		"alertNamePattern": "Phishing%", "isDefault": true,
	})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var pb domain.Playbook
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pb))
	assert.Equal(t, "Phishing%", pb.AlertNamePattern)
	assert.True(t, pb.IsDefault)

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

func TestPlaybookHandlers_TriggerStepWebhook(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	alertRepo := repository.NewAlertRepository()
	h := handlers.NewPlaybookHandlers(service.NewPlaybookService(pool, repository.NewPlaybookRepository(), alertRepo, "https://argusops.example"))
	r := newRouter(h.Routes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{
		"title": "Ransomware Response", "category": "Ransomware",
		"steps": map[string]any{
			"containment": []map[string]any{{"text": "Isolate host", "webhookUrl": srv.URL}},
		},
	})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var pb domain.Playbook
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pb))

	// create's response never has the DB-generated step ids (replaceSteps is
	// a delete-then-reinsert with nothing to scan back) -- re-fetch to learn
	// the real one.
	getReq := withClaims(httptest.NewRequest("GET", "/"+pb.ID.String(), nil), tenantID, actorID, nil)
	getRec := doRequest(r, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)
	var fetched domain.Playbook
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &fetched))
	stepID := fetched.Steps[domain.PhaseContainment][0].ID.String()

	var alertID string
	require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		a := &domain.Alert{
			TenantID: tenantID, Title: "Suspicious login", Source: "wazuh",
			Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh,
			Status: domain.AlertStatusOpen, Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
		}
		if err := alertRepo.Insert(t.Context(), tx, a); err != nil {
			return err
		}
		alertID = a.ID.String()
		return nil
	}))

	t.Run("invalid step id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"alertId": alertID})
		req := withClaims(httptest.NewRequest("POST", "/steps/not-a-uuid/trigger", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("missing alertId -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/steps/"+stepID+"/trigger", bytes.NewReader([]byte("{}"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/steps/"+stepID+"/trigger", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("fires the webhook -- 204", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"alertId": alertID})
		req := withClaims(httptest.NewRequest("POST", "/steps/"+stepID+"/trigger", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})

	t.Run("a step with no webhook configured -- 502", func(t *testing.T) {
		noHookBody, _ := json.Marshal(map[string]any{
			"title": "No Hook", "category": "Test",
			"steps": map[string]any{"containment": []map[string]any{{"text": "Just text"}}},
		})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(noHookBody)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusCreated, rec.Code)
		var noHookPb domain.Playbook
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &noHookPb))

		getReq := withClaims(httptest.NewRequest("GET", "/"+noHookPb.ID.String(), nil), tenantID, actorID, nil)
		getRec := doRequest(r, getReq)
		require.Equal(t, http.StatusOK, getRec.Code)
		var fetchedNoHook domain.Playbook
		require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &fetchedNoHook))
		noHookStepID := fetchedNoHook.Steps[domain.PhaseContainment][0].ID.String()

		body, _ := json.Marshal(map[string]string{"alertId": alertID})
		req = withClaims(httptest.NewRequest("POST", "/steps/"+noHookStepID+"/trigger", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadGateway, doRequest(r, req).Code)
	})
}
