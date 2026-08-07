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
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newIncidentHandlerFixture(t *testing.T) (h *handlers.IncidentHandlers, tenantID, actorID, incidentID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "analyst", nil)

	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	incidentRepo := repository.NewIncidentRepository()
	incSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	userSvc := service.NewUserService(pool, repository.NewUserRepository())
	secretStore := secrets.NewEnvStore()
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), repository.NewAlertRepository(), incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	h = handlers.NewIncidentHandlers(incSvc, userSvc, aiSvc)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Ransomware suspected", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
	})
	require.NoError(t, err)
	return h, tenantID, actorID, inc.ID
}

func TestIncidentHandlers_Analyze_NoProviderConfigured(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/analyze", nil), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "no LLM provider configured")
}

func TestIncidentHandlers_ListAndCreate(t *testing.T) {
	h, tenantID, actorID, _ := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("list", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Len(t, incidents, 1)
	})

	t.Run("create with missing title -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"description": "no title"})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("create succeeds -- 201", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"title": "Phishing wave", "severity": "high", "priority": "p2"})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusCreated, rec.Code)
	})

	t.Run("create with an unknown assigneeId -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"title": "Bad incident", "severity": "low", "priority": "p4", "assigneeIds": []string{uuid.New().String()}})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("create with a valid assigneeId -- 201, assignee resolved", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"title": "Assigned incident", "severity": "medium", "priority": "p3", "assigneeIds": []string{actorID.String()}})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusCreated, rec.Code)
		var inc domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &inc))
		require.Len(t, inc.Assignees, 1)
		assert.Equal(t, actorID, inc.Assignees[0].ID)
	})
}

func TestIncidentHandlers_SetAssignees(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)
	otherAnalyst := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("replaces the assignee set", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"assigneeIds": []string{actorID.String(), otherAnalyst.String()}})
		req := withClaims(httptest.NewRequest("PUT", "/"+incidentID.String()+"/assignees", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		getReq := withClaims(httptest.NewRequest("GET", "/"+incidentID.String(), nil), tenantID, actorID, nil)
		getRec := doRequest(r, getReq)
		var inc domain.Incident
		require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &inc))
		require.Len(t, inc.Assignees, 2)
	})

	t.Run("unknown assignee id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"assigneeIds": []string{uuid.New().String()}})
		req := withClaims(httptest.NewRequest("PUT", "/"+incidentID.String()+"/assignees", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestIncidentHandlers_Get(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/"+incidentID.String(), nil), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	t.Run("unknown -- 404", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+uuid.New().String(), nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestIncidentHandlers_ChangePhaseAndClose(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"phase": "containment"})
	req := withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/phase", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	req = withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/close", nil), tenantID, actorID, nil)
	rec = doRequest(r, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestIncidentHandlers_SetSeverityPriorityAndDescriptionAndTags(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"severity": "low", "priority": "p4"})
	req := withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/severity-priority", bytes.NewReader(body)), tenantID, actorID, nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	body, _ = json.Marshal(map[string]string{"description": "updated"})
	req = withClaims(httptest.NewRequest("PUT", "/"+incidentID.String()+"/description", bytes.NewReader(body)), tenantID, actorID, nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	body, _ = json.Marshal(map[string][]string{"tags": {}})
	req = withClaims(httptest.NewRequest("PUT", "/"+incidentID.String()+"/tags", bytes.NewReader(body)), tenantID, actorID, nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
}

func TestIncidentHandlers_StatusHistoryAndCorrect(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/"+incidentID.String()+"/status-history", nil), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	t.Run("correction with empty reason -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"enteredAt": "2026-01-01T00:00:00Z", "reason": ""})
		req := withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/status-history/new/correct", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("valid correction -- 204", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"enteredAt": "2026-01-01T00:00:00Z", "reason": "backdated"})
		req := withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/status-history/new/correct", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNoContent, rec.Code)
	})
}

func TestIncidentHandlers_TimelineCommentsAndAlertLinks(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/"+incidentID.String()+"/timeline", nil), tenantID, actorID, nil)
	assert.Equal(t, http.StatusOK, doRequest(r, req).Code)

	t.Run("comments", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"body": ""})
		req := withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/comments", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code, "empty body is rejected")

		body, _ = json.Marshal(map[string]string{"body": "investigating"})
		req = withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/comments", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusCreated, doRequest(r, req).Code)

		req = withClaims(httptest.NewRequest("GET", "/"+incidentID.String()+"/comments", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var comments []domain.IncidentComment
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &comments))
		assert.Len(t, comments, 1)
	})

	t.Run("alert links", func(t *testing.T) {
		tagSvc := service.NewTagService(pool, repository.NewTagRepository())
		alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "a", Source: "s", Severity: domain.SeverityLow, Payload: json.RawMessage(`{}`),
		})
		require.NoError(t, err)

		req := withClaims(httptest.NewRequest("PUT", "/"+incidentID.String()+"/alerts/"+alert.ID.String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		req = withClaims(httptest.NewRequest("GET", "/"+incidentID.String()+"/alerts", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var linked []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &linked))
		require.Len(t, linked, 1)

		req = withClaims(httptest.NewRequest("DELETE", "/"+incidentID.String()+"/alerts/"+alert.ID.String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}
