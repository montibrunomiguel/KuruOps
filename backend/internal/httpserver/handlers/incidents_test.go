package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	incidentRepo := repository.NewIncidentRepository()
	incSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	userSvc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())
	secretStore := secrets.NewEnvStore()
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), repository.NewAlertRepository(), incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	postmortemSvc := service.NewPostmortemService(incSvc, aiSvc)
	h = handlers.NewIncidentHandlers(incSvc, userSvc, aiSvc, postmortemSvc, mcpToolSvc)

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

// TestIncidentHandlers_Analyze_ReturnsImmediatelyThenCompletesInBackground
// mirrors TestAlertHandlers_Analyze_ReturnsImmediatelyThenCompletesInBackground
// -- confirms IncidentService's own (new) LatestAnalysis*/EnableAnalysisLookup
// wiring works the same way AlertService's already did.
func TestIncidentHandlers_Analyze_ReturnsImmediatelyThenCompletesInBackground(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	secretStore := secrets.NewEnvStore()

	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	incidentRepo := repository.NewIncidentRepository()
	incSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	incSvc.EnableAnalysisLookup(repository.NewAIAnalysisRunRepository())
	userSvc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), repository.NewAlertRepository(), incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	analyzed := make(chan struct{}, 1)
	aiSvc.EnableEventPublishing(func(uuid.UUID, string, any) { analyzed <- struct{}{} })
	postmortemSvc := service.NewPostmortemService(incSvc, aiSvc)
	h := handlers.NewIncidentHandlers(incSvc, userSvc, aiSvc, postmortemSvc, mcpToolSvc)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Ransomware suspected", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recommend containment"}}]}`))
	}))
	defer srv.Close()
	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secretStore, repository.NewAdminAuditEventRepository())
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, actorID, provider.ID))

	r := newRouter(h.Routes)
	req := withClaims(httptest.NewRequest("POST", "/"+inc.ID.String()+"/analyze", nil), tenantID, actorID, nil)
	rec := doRequest(r, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "running", body["status"])

	select {
	case <-analyzed:
	case <-time.After(2 * time.Second):
		t.Fatal("background analysis did not complete in time")
	}

	got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, got.LatestAnalysisStatus)
	assert.Equal(t, "completed", *got.LatestAnalysisStatus)
	require.NotNil(t, got.LatestAnalysis)
	assert.Equal(t, "recommend containment", *got.LatestAnalysis)
}

func TestIncidentHandlers_ListAndCreate(t *testing.T) {
	h, tenantID, actorID, _ := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("list", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "1", rec.Header().Get("X-Total-Count"))
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

func TestIncidentHandlers_SetRole(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)
	otherAnalyst := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("assigns a single-assignee role", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"userIds": []string{actorID.String()}})
		req := withClaims(httptest.NewRequest("PUT", "/"+incidentID.String()+"/roles/commander", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		getReq := withClaims(httptest.NewRequest("GET", "/"+incidentID.String(), nil), tenantID, actorID, nil)
		var inc domain.Incident
		require.NoError(t, json.Unmarshal(doRequest(r, getReq).Body.Bytes(), &inc))
		require.Len(t, inc.Roles, 1)
		assert.Equal(t, domain.RoleCommander, inc.Roles[0].Role)
	})

	t.Run("two people for commander -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"userIds": []string{actorID.String(), otherAnalyst.String()}})
		req := withClaims(httptest.NewRequest("PUT", "/"+incidentID.String()+"/roles/commander", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "at most one person")
	})

	t.Run("unknown role -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"userIds": []string{actorID.String()}})
		req := withClaims(httptest.NewRequest("PUT", "/"+incidentID.String()+"/roles/not-a-role", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("multi-assignee role accepts several people", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"userIds": []string{actorID.String(), otherAnalyst.String()}})
		req := withClaims(httptest.NewRequest("PUT", "/"+incidentID.String()+"/roles/incident_handler", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		getReq := withClaims(httptest.NewRequest("GET", "/"+incidentID.String(), nil), tenantID, actorID, nil)
		var inc domain.Incident
		require.NoError(t, json.Unmarshal(doRequest(r, getReq).Body.Bytes(), &inc))
		handlerCount := 0
		for _, ra := range inc.Roles {
			if ra.Role == domain.RoleIncidentHandler {
				handlerCount++
			}
		}
		assert.Equal(t, 2, handlerCount)
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

func TestIncidentHandlers_Postmortem(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/"+incidentID.String()+"/postmortem", nil), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/markdown; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="postmortem-`+incidentID.String()+`.md"`, rec.Header().Get("Content-Disposition"))
	assert.Contains(t, rec.Body.String(), "# Postmortem: Ransomware suspected")

	t.Run("unknown -- 404", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+uuid.New().String()+"/postmortem", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("malformed id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/not-a-uuid/postmortem", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
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
		tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
		alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())
		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "a", Source: "s", Severity: domain.SeverityLow, Payload: json.RawMessage(`{}`),
		}, nil, 0)
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

func TestIncidentHandlers_List_Filters(t *testing.T) {
	h, tenantID, actorID, _ := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("severity filter matches", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?severity=critical", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Len(t, incidents, 1)
	})

	t.Run("priority filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?priority=p1", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Len(t, incidents, 1)
	})

	t.Run("phase filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?phase=new", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Len(t, incidents, 1)
	})

	t.Run("tag filter excludes an untagged incident", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?tag=phishing", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Empty(t, incidents)
	})

	t.Run("sla=breached excludes an incident that hasn't breached", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?sla=breached", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "0", rec.Header().Get("X-Total-Count"))
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Empty(t, incidents)
	})

	t.Run("sla=ok includes an incident that hasn't breached", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?sla=ok", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Len(t, incidents, 1)
	})

	t.Run("commanderId filter with no commander set yet excludes it", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?commanderId="+actorID.String(), nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Empty(t, incidents)
	})

	t.Run("since filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?since=2000-01-01T00:00:00Z", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Len(t, incidents, 1)
	})

	t.Run("until filter excludes an incident opened before it", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?until=2000-01-01T00:00:00Z", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Empty(t, incidents)
	})

	t.Run("until filter includes an incident opened before it", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?until=2999-01-01T00:00:00Z", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Len(t, incidents, 1)
	})
}

func TestIncidentHandlers_MalformedID_Returns400(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"get", httptest.NewRequest("GET", "/not-a-uuid", nil)},
		{"changePhase", httptest.NewRequest("POST", "/not-a-uuid/phase", bytes.NewReader([]byte(`{}`)))},
		{"close", httptest.NewRequest("POST", "/not-a-uuid/close", nil)},
		{"setSeverityPriority", httptest.NewRequest("POST", "/not-a-uuid/severity-priority", bytes.NewReader([]byte(`{}`)))},
		{"updateDescription", httptest.NewRequest("PUT", "/not-a-uuid/description", bytes.NewReader([]byte(`{}`)))},
		{"updateTags", httptest.NewRequest("PUT", "/not-a-uuid/tags", bytes.NewReader([]byte(`{}`)))},
		{"setAssignees", httptest.NewRequest("PUT", "/not-a-uuid/assignees", bytes.NewReader([]byte(`{}`)))},
		{"setRole", httptest.NewRequest("PUT", "/not-a-uuid/roles/commander", bytes.NewReader([]byte(`{}`)))},
		{"statusHistory", httptest.NewRequest("GET", "/not-a-uuid/status-history", nil)},
		{"correctPhaseTimestamp", httptest.NewRequest("POST", "/not-a-uuid/status-history/new/correct", bytes.NewReader([]byte(`{}`)))},
		{"timeline", httptest.NewRequest("GET", "/not-a-uuid/timeline", nil)},
		{"listComments", httptest.NewRequest("GET", "/not-a-uuid/comments", nil)},
		{"addComment", httptest.NewRequest("POST", "/not-a-uuid/comments", bytes.NewReader([]byte(`{}`)))},
		{"linkedAlerts", httptest.NewRequest("GET", "/not-a-uuid/alerts", nil)},
		{"linkAlert malformed id", httptest.NewRequest("PUT", "/not-a-uuid/alerts/"+uuid.New().String(), nil)},
		{"linkAlert malformed alertId", httptest.NewRequest("PUT", "/"+incidentID.String()+"/alerts/not-a-uuid", nil)},
		{"unlinkAlert malformed id", httptest.NewRequest("DELETE", "/not-a-uuid/alerts/"+uuid.New().String(), nil)},
		{"unlinkAlert malformed alertId", httptest.NewRequest("DELETE", "/"+incidentID.String()+"/alerts/not-a-uuid", nil)},
		{"analyze", httptest.NewRequest("POST", "/not-a-uuid/analyze", nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withClaims(tc.req, tenantID, actorID, nil)
			rec := doRequest(r, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestIncidentHandlers_MalformedBody_Returns400(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)
	badBody := bytes.NewReader([]byte("not json"))

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"changePhase", httptest.NewRequest("POST", "/"+incidentID.String()+"/phase", badBody)},
		{"setSeverityPriority", httptest.NewRequest("POST", "/"+incidentID.String()+"/severity-priority", bytes.NewReader([]byte("not json")))},
		{"updateDescription", httptest.NewRequest("PUT", "/"+incidentID.String()+"/description", bytes.NewReader([]byte("not json")))},
		{"updateTags", httptest.NewRequest("PUT", "/"+incidentID.String()+"/tags", bytes.NewReader([]byte("not json")))},
		{"setAssignees", httptest.NewRequest("PUT", "/"+incidentID.String()+"/assignees", bytes.NewReader([]byte("not json")))},
		{"setRole", httptest.NewRequest("PUT", "/"+incidentID.String()+"/roles/commander", bytes.NewReader([]byte("not json")))},
		{"correctPhaseTimestamp", httptest.NewRequest("POST", "/"+incidentID.String()+"/status-history/new/correct", bytes.NewReader([]byte("not json")))},
		{"addComment", httptest.NewRequest("POST", "/"+incidentID.String()+"/comments", bytes.NewReader([]byte("not json")))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withClaims(tc.req, tenantID, actorID, nil)
			rec := doRequest(r, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// TestIncidentHandlers_Close_Twice documents that, unlike alerts.Close,
// closing an incident is idempotent: IncidentService.ChangePhase treats
// "already at the target phase" as a no-op (current.Phase == newPhase
// returns nil rather than an error), so a second close still returns 204,
// not 400/409.
func TestIncidentHandlers_Close_Twice(t *testing.T) {
	h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/close", nil), tenantID, actorID, nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	t.Run("closing twice is idempotent -- still 204", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/close", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}

func TestIncidentHandlers_AddComment_UnknownActor(t *testing.T) {
	h, tenantID, _, incidentID := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"body": "hi"})
	req := withClaims(httptest.NewRequest("POST", "/"+incidentID.String()+"/comments", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestIncidentHandlers_List_MissingTenantContext(t *testing.T) {
	h, _, _, _ := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
