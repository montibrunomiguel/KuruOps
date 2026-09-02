package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func newAlertHandlerFixture(t *testing.T) (h *handlers.AlertHandlers, tenantID uuid.UUID, actorID uuid.UUID, alertID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "analyst", nil)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)

	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc, repository.NewPlaybookRepository())
	incidentSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	secretStore := secrets.NewEnvStore()
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), alertRepo, incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	userSvc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())
	onCallScheduleRepo := repository.NewOnCallScheduleRepository()
	onCallSvc := service.NewOnCallScheduleService(pool, onCallScheduleRepo, repository.NewUserRepository(), repository.NewTenantRepository(), repository.NewAdminAuditEventRepository())
	escalationPolicySvc := service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), onCallScheduleRepo, onCallSvc, userSvc, secretStore, repository.NewAdminAuditEventRepository())
	alertSvc.EnableEscalation(incidentSvc, escalationPolicySvc, "http://localhost:3000")
	h = handlers.NewAlertHandlers(alertSvc, aiSvc, mcpToolSvc, userSvc)

	alert, _, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
		Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh,
		Payload: json.RawMessage(`{}`),
	}, nil, 0)
	require.NoError(t, err)
	return h, tenantID, actorID, alert.ID
}

// newSecondAlert ingests one more alert into tenantID -- used by tests that
// need two distinct alerts to link together, on top of the one
// newAlertHandlerFixture already creates.
func newSecondAlert(t *testing.T, h *handlers.AlertHandlers, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository()), repository.NewPlaybookRepository())
	a, _, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
		Title: "Second alert", Source: "wazuh", Severity: domain.SeverityMedium,
		Payload: json.RawMessage(`{}`),
	}, nil, 0)
	require.NoError(t, err)
	return a.ID
}

func TestAlertHandlers_List(t *testing.T) {
	h, tenantID, _, _ := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var alerts []domain.Alert
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
	assert.Len(t, alerts, 1)
}

// TestAlertHandlers_List_TotalCountHeader is the regression test for
// page-number pagination: the total-row-count must reflect the applied
// filter and ignore limit/offset, so the frontend can compute total pages
// independent of which page it's currently viewing.
func TestAlertHandlers_List_TotalCountHeader(t *testing.T) {
	h, tenantID, _, _ := newAlertHandlerFixture(t)
	newSecondAlert(t, h, tenantID)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/?limit=1", nil), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "2", rec.Header().Get("X-Total-Count"))
	var alerts []domain.Alert
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
	assert.Len(t, alerts, 1, "the page itself is still limited to 1")
}

func TestAlertHandlers_Analyze_NoProviderConfigured(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/analyze", nil), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "no LLM provider configured")
}

// TestAlertHandlers_Analyze_ReturnsImmediatelyThenCompletesInBackground is
// the handler-level regression test for the whole point of this frente:
// POST /analyze responds 202 right away (not after the LLM call finishes),
// and the eventual result only shows up via a later GET (see
// domain.Alert.LatestAnalysis*), driven by the same completion signal a
// real SSE Broadcaster would fan out.
func TestAlertHandlers_Analyze_ReturnsImmediatelyThenCompletesInBackground(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	secretStore := secrets.NewEnvStore()

	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc, repository.NewPlaybookRepository())
	alertSvc.EnableAnalysisLookup(repository.NewAIAnalysisRunRepository())
	incidentSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), alertRepo, incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	analyzed := make(chan struct{}, 1)
	aiSvc.EnableEventPublishing(func(uuid.UUID, string, any) { analyzed <- struct{}{} })
	userSvc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())
	onCallScheduleRepo := repository.NewOnCallScheduleRepository()
	onCallSvc := service.NewOnCallScheduleService(pool, onCallScheduleRepo, repository.NewUserRepository(), repository.NewTenantRepository(), repository.NewAdminAuditEventRepository())
	escalationPolicySvc := service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), onCallScheduleRepo, onCallSvc, userSvc, secretStore, repository.NewAdminAuditEventRepository())
	alertSvc.EnableEscalation(incidentSvc, escalationPolicySvc, "http://localhost:3000")
	h := handlers.NewAlertHandlers(alertSvc, aiSvc, mcpToolSvc, userSvc)

	alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
	}, nil, 0)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"likely benign"}}]}`))
	}))
	defer srv.Close()
	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secretStore, repository.NewAdminAuditEventRepository())
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, actorID, provider.ID))

	r := newRouter(h.Routes)
	req := withClaims(httptest.NewRequest("POST", "/"+alert.ID.String()+"/analyze", nil), tenantID, actorID, nil)
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

	got, err := alertSvc.Get(t.Context(), tenantID, alert.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, got.LatestAnalysisStatus)
	assert.Equal(t, "completed", *got.LatestAnalysisStatus)
	require.NotNil(t, got.LatestAnalysis)
	assert.Equal(t, "likely benign", *got.LatestAnalysis)
}

// TestAlertHandlers_Analyze_AlreadyInProgress confirms the handler surfaces
// service.ErrAnalysisInProgress as 409, not the generic 400 every other
// validation failure gets.
func TestAlertHandlers_Analyze_AlreadyInProgress(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	secretStore := secrets.NewEnvStore()

	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc, repository.NewPlaybookRepository())
	incidentSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), alertRepo, incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	analyzed := make(chan struct{}, 1)
	aiSvc.EnableEventPublishing(func(uuid.UUID, string, any) { analyzed <- struct{}{} })
	userSvc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())
	onCallScheduleRepo := repository.NewOnCallScheduleRepository()
	onCallSvc := service.NewOnCallScheduleService(pool, onCallScheduleRepo, repository.NewUserRepository(), repository.NewTenantRepository(), repository.NewAdminAuditEventRepository())
	escalationPolicySvc := service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), onCallScheduleRepo, onCallSvc, userSvc, secretStore, repository.NewAdminAuditEventRepository())
	alertSvc.EnableEscalation(incidentSvc, escalationPolicySvc, "http://localhost:3000")
	h := handlers.NewAlertHandlers(alertSvc, aiSvc, mcpToolSvc, userSvc)

	alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
	}, nil, 0)
	require.NoError(t, err)

	// A slow LLM double -- long enough that the first analysis is still
	// running when the second /analyze request arrives.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"..."}}]}`))
	}))
	defer srv.Close()
	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secretStore, repository.NewAdminAuditEventRepository())
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, actorID, provider.ID))

	r := newRouter(h.Routes)
	first := withClaims(httptest.NewRequest("POST", "/"+alert.ID.String()+"/analyze", nil), tenantID, actorID, nil)
	require.Equal(t, http.StatusAccepted, doRequest(r, first).Code)

	second := withClaims(httptest.NewRequest("POST", "/"+alert.ID.String()+"/analyze", nil), tenantID, actorID, nil)
	rec := doRequest(r, second)
	assert.Equal(t, http.StatusConflict, rec.Code)

	select {
	case <-analyzed:
	case <-time.After(2 * time.Second):
		t.Fatal("background analysis did not complete in time")
	}
}

func TestAlertHandlers_List_CorrelatedFilter(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("correlated=false includes the not-yet-escalated fixture alert", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?correlated=false", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Len(t, alerts, 1)
	})

	t.Run("correlated=true excludes it until it's escalated into an incident", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?correlated=true", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Empty(t, alerts)
	})

	// Regression test for the "Correlated Alerts" bug: escalate already
	// links the alert via IncidentService.LinkAlert (incident_alert_links),
	// but the Correlated filter used to read a separate, never-written
	// alerts.incident_id column -- so it never picked this up. Now that the
	// filter queries incident_alert_links directly, escalating alone (no
	// other code change) must be enough to flip it.
	t.Run("correlated=true includes it once escalated, with no changes needed to the escalate flow itself", func(t *testing.T) {
		escalateReq := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/escalate", nil), tenantID, actorID, nil)
		escalateRec := doRequest(r, escalateReq)
		require.Equal(t, http.StatusOK, escalateRec.Code, escalateRec.Body.String())

		req := withClaims(httptest.NewRequest("GET", "/?correlated=true", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		require.Len(t, alerts, 1)
		assert.Equal(t, alertID, alerts[0].ID)
		require.NotNil(t, alerts[0].IncidentID)
	})
}

func TestAlertHandlers_Get(t *testing.T) {
	h, tenantID, _, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("found", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+alertID.String(), nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("unknown id -- 404", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+uuid.New().String(), nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("malformed id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/not-a-uuid", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("out-of-scope tag -- 404, same as not found", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+alertID.String(), nil), tenantID, uuid.New(), []string{"unrelated-tag"})
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestAlertHandlers_ChangeStatus(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("valid transition -- 204", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"status": "investigating"})
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/status", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("direct transition to closed is rejected -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"status": "closed"})
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/status", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("malformed body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/status", bytes.NewReader([]byte("not json"))), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("malformed id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/status", bytes.NewReader([]byte(`{}`))), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestAlertHandlers_BulkChangeStatus(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)
	otherID := newSecondAlert(t, h, tenantID)

	t.Run("valid ids -- 200 with one result per id", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"ids": []string{alertID.String(), otherID.String()}, "status": "investigating"})
		req := withClaims(httptest.NewRequest("POST", "/bulk/status", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Results []struct {
				ID      string `json:"id"`
				Success bool   `json:"success"`
				Error   string `json:"error"`
			} `json:"results"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Results, 2)
		for _, res := range resp.Results {
			assert.True(t, res.Success)
		}
	})

	t.Run("a bad id among good ones still returns 200, with that one marked failed", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"ids": []string{alertID.String(), uuid.New().String()}, "status": "escalated"})
		req := withClaims(httptest.NewRequest("POST", "/bulk/status", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Results []struct {
				Success bool `json:"success"`
			} `json:"results"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Results, 2)
		successes := 0
		for _, res := range resp.Results {
			if res.Success {
				successes++
			}
		}
		assert.Equal(t, 1, successes)
	})

	t.Run("empty ids -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"ids": []string{}, "status": "investigating"})
		req := withClaims(httptest.NewRequest("POST", "/bulk/status", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("malformed body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/bulk/status", bytes.NewReader([]byte("not json"))), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestAlertHandlers_Close(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"classification": "true_positive", "comment": "confirmed"})
	req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/close", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	t.Run("closing twice -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/close", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestAlertHandlers_UpdateTags(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string][]string{"tags": {"phishing"}})
	req := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/tags", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestAlertHandlers_OverrideSeverity(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("valid override -- 204", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"severity": "critical"})
		req := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/severity", bytes.NewReader(body)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("malformed body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/severity", bytes.NewReader([]byte("not json"))), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestAlertHandlers_LinkUnlinkAlerts(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("link, list, unlink", func(t *testing.T) {
		otherID := newSecondAlert(t, h, tenantID)

		linkReq := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/alerts/"+otherID.String(), nil), tenantID, actorID, nil)
		linkRec := doRequest(r, linkReq)
		assert.Equal(t, http.StatusNoContent, linkRec.Code)

		listReq := withClaims(httptest.NewRequest("GET", "/"+alertID.String()+"/alerts", nil), tenantID, actorID, nil)
		listRec := doRequest(r, listReq)
		assert.Equal(t, http.StatusOK, listRec.Code)
		var linked []domain.Alert
		require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &linked))
		require.Len(t, linked, 1)
		assert.Equal(t, otherID, linked[0].ID)

		unlinkReq := withClaims(httptest.NewRequest("DELETE", "/"+alertID.String()+"/alerts/"+otherID.String(), nil), tenantID, actorID, nil)
		unlinkRec := doRequest(r, unlinkReq)
		assert.Equal(t, http.StatusNoContent, unlinkRec.Code)

		listReq2 := withClaims(httptest.NewRequest("GET", "/"+alertID.String()+"/alerts", nil), tenantID, actorID, nil)
		listRec2 := doRequest(r, listReq2)
		var linked2 []domain.Alert
		require.NoError(t, json.Unmarshal(listRec2.Body.Bytes(), &linked2))
		assert.Empty(t, linked2)
	})

	t.Run("linking to itself -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/alerts/"+alertID.String(), nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestAlertHandlers_Escalate(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/escalate", nil), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		IncidentID uuid.UUID `json:"incidentId"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEqual(t, uuid.Nil, resp.IncidentID)

	t.Run("the alert's status moves to escalated", func(t *testing.T) {
		getReq := withClaims(httptest.NewRequest("GET", "/"+alertID.String(), nil), tenantID, actorID, nil)
		getRec := doRequest(r, getReq)
		var alert domain.Alert
		require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &alert))
		assert.Equal(t, domain.AlertStatusEscalated, alert.Status)
	})

	t.Run("the new incident's priority is seeded from the alert's severity, not hardcoded p3", func(t *testing.T) {
		pool := testutil.RequireTestDB(t)
		incidentSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository()), repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
		incident, err := incidentSvc.Get(t.Context(), tenantID, resp.IncidentID, nil)
		require.NoError(t, err)
		require.NotNil(t, incident)
		// the fixture alert is domain.SeverityHigh -- see newAlertHandlerFixture.
		assert.Equal(t, domain.PriorityP2, incident.Priority)
	})

	t.Run("unknown alert id -- 404", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+uuid.New().String()+"/escalate", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("escalating the same alert again -- 409, not a second incident", func(t *testing.T) {
		// The alert was escalated at the top of this test, so this is the
		// double-click/retry path.
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/escalate", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusConflict, rec.Code)
	})
}

func TestAlertHandlers_List_MissingTenantContext(t *testing.T) {
	h, _, _, _ := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAlertHandlers_Get_MissingTenantContext(t *testing.T) {
	h, _, _, _ := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/"+uuid.New().String(), nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAlertHandlers_ResolveAnalysisToolCall covers resolveAnalysisToolCall's
// error branches (internal/httpserver/handlers/analysis_tool_calls.go),
// shared by AlertHandlers and IncidentHandlers -- the happy-path approve/
// reject flow is exercised elsewhere via the full AnalysisChat flow, but
// nothing previously hit these guard clauses directly.
func TestAlertHandlers_ResolveAnalysisToolCall(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("non-numeric call id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/analyze/tool-calls/not-a-number/approve", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("unknown call id -- 404", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/analyze/tool-calls/999999/approve", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("unknown call id -- reject also 404", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/analyze/tool-calls/999999/reject", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestAlertHandlers_Comments(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"body": ""})
	req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/comments", bytes.NewReader(body)), tenantID, actorID, nil)
	assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code, "empty body is rejected")

	body, _ = json.Marshal(map[string]string{"body": "confirmed source IP is a known scanner"})
	req = withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/comments", bytes.NewReader(body)), tenantID, actorID, nil)
	assert.Equal(t, http.StatusCreated, doRequest(r, req).Code)

	req = withClaims(httptest.NewRequest("GET", "/"+alertID.String()+"/comments", nil), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	var comments []domain.AlertComment
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &comments))
	require.Len(t, comments, 1)
	assert.Equal(t, "confirmed source IP is a known scanner", comments[0].Body)

	// This used to assert 200 + an empty list, back when listComments
	// queried by ID under tenant RLS alone and so had no idea whether the
	// alert existed. Now that it re-checks visibility (see
	// AlertService.Comments), an unknown ID and a tag-invisible one both
	// answer 404 -- the same deliberate opacity AlertService.Get's doc
	// comment describes, so a caller can't distinguish "no such alert" from
	// "exists but you can't see it" by response shape.
	t.Run("unknown alert id -- 404, indistinguishable from a hidden one", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+uuid.New().String()+"/comments", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("an alert the caller's tags don't cover -- same 404", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+alertID.String()+"/comments", nil), tenantID, actorID, []string{"some-other-team"})
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.NotContains(t, rec.Body.String(), "confirmed source IP is a known scanner",
			"the comment body must not leak to a caller who cannot see the alert")
	})
}

func TestAlertHandlers_List_Filters(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("severity filter matches the fixture alert", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?severity=high", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Len(t, alerts, 1)
	})

	t.Run("severity filter excludes a non-matching severity", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?severity=low", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Empty(t, alerts)
	})

	t.Run("status filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?status=open", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Len(t, alerts, 1)
	})

	t.Run("source filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?source=wazuh", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Len(t, alerts, 1)
	})

	t.Run("whitespace-only q behaves like no search at all, not a zero-result search", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?q=%20%20%20", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Len(t, alerts, 1)
	})

	t.Run("tag filter excludes an untagged alert", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?tag=phishing", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Empty(t, alerts)
	})

	t.Run("since filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?since=2000-01-01T00:00:00Z", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Len(t, alerts, 1)
	})

	t.Run("until filter excludes an alert received before it", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?until=2000-01-01T00:00:00Z", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Empty(t, alerts)
	})

	t.Run("until filter includes an alert received before it", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?until=2999-01-01T00:00:00Z", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Len(t, alerts, 1)
	})

	t.Run("q filter matches a word in the title", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?q=login", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Len(t, alerts, 1)
	})

	t.Run("q filter excludes a non-matching term", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/?q=ransomware", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Empty(t, alerts)
	})

	_ = alertID
}

func TestAlertHandlers_MalformedID_Returns400(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)
	otherID := newSecondAlert(t, h, tenantID)

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"close", httptest.NewRequest("POST", "/not-a-uuid/close", bytes.NewReader([]byte(`{}`)))},
		{"updateTags malformed id", httptest.NewRequest("PUT", "/not-a-uuid/tags", bytes.NewReader([]byte(`{}`)))},
		{"overrideSeverity malformed id", httptest.NewRequest("PUT", "/not-a-uuid/severity", bytes.NewReader([]byte(`{}`)))},
		{"reassign malformed id", httptest.NewRequest("PUT", "/not-a-uuid/assignee", bytes.NewReader([]byte(`{}`)))},
		{"listLinkedAlerts malformed id", httptest.NewRequest("GET", "/not-a-uuid/alerts", nil)},
		{"linkAlert malformed id", httptest.NewRequest("PUT", "/not-a-uuid/alerts/"+otherID.String(), nil)},
		{"linkAlert malformed otherId", httptest.NewRequest("PUT", "/"+alertID.String()+"/alerts/not-a-uuid", nil)},
		{"unlinkAlert malformed id", httptest.NewRequest("DELETE", "/not-a-uuid/alerts/"+otherID.String(), nil)},
		{"unlinkAlert malformed otherId", httptest.NewRequest("DELETE", "/"+alertID.String()+"/alerts/not-a-uuid", nil)},
		{"escalate malformed id", httptest.NewRequest("POST", "/not-a-uuid/escalate", nil)},
		{"analyze malformed id", httptest.NewRequest("POST", "/not-a-uuid/analyze", nil)},
		{"listComments malformed id", httptest.NewRequest("GET", "/not-a-uuid/comments", nil)},
		{"addComment malformed id", httptest.NewRequest("POST", "/not-a-uuid/comments", bytes.NewReader([]byte(`{}`)))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withClaims(tc.req, tenantID, actorID, nil)
			rec := doRequest(r, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestAlertHandlers_Close_MalformedBody(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/close", bytes.NewReader([]byte("not json"))), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAlertHandlers_UpdateTags_MalformedBody(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/tags", bytes.NewReader([]byte("not json"))), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAlertHandlers_Reassign_MalformedBody(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/assignee", bytes.NewReader([]byte("not json"))), tenantID, actorID, nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAlertHandlers_AddComment_MalformedBodyAndUnknownActor(t *testing.T) {
	h, tenantID, _, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("malformed body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/comments", bytes.NewReader([]byte("not json"))), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("actor not found -- 401", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"body": "hi"})
		req := withClaims(httptest.NewRequest("POST", "/"+alertID.String()+"/comments", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestAlertHandlers_Reassign(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)

	body, _ := json.Marshal(map[string]string{"analystId": analystID.String()})
	req := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/assignee", bytes.NewReader(body)), tenantID, actorID, nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	getReq := withClaims(httptest.NewRequest("GET", "/"+alertID.String(), nil), tenantID, actorID, nil)
	getRec := doRequest(r, getReq)
	var alert domain.Alert
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &alert))
	require.NotNil(t, alert.AssignedAnalystID)
	assert.Equal(t, analystID, *alert.AssignedAnalystID)

	t.Run("clearing the assignee with a null analystId", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"analystId": nil})
		req := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/assignee", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		getRec := doRequest(r, withClaims(httptest.NewRequest("GET", "/"+alertID.String(), nil), tenantID, actorID, nil))
		var alert domain.Alert
		require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &alert))
		assert.Nil(t, alert.AssignedAnalystID)
	})
}

// TestAlertHandlers_ReassignRequiresAnalystIDKey guards the difference
// between "unassign this alert" and "I got the field name wrong". AnalystID
// is a pointer so an explicit null can mean unassign, which used to make a
// body carrying no recognised key look identical to a deliberate unassign:
// a client sending the wrong name got a 204 and a silently cleared
// assignee.
func TestAlertHandlers_ReassignRequiresAnalystIDKey(t *testing.T) {
	h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	put := func(t *testing.T, body string) int {
		t.Helper()
		req := withClaims(httptest.NewRequest("PUT", "/"+alertID.String()+"/assignee", strings.NewReader(body)), tenantID, actorID, nil)
		req.Header.Set("Content-Type", "application/json")
		return doRequest(r, req).Code
	}

	t.Run("assigning with the right key works", func(t *testing.T) {
		assert.Equal(t, http.StatusNoContent, put(t, `{"analystId":"`+actorID.String()+`"}`))
	})

	t.Run("an explicit null still unassigns", func(t *testing.T) {
		assert.Equal(t, http.StatusNoContent, put(t, `{"analystId":null}`))
	})

	t.Run("a body with the wrong field name is refused", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, put(t, `{"assigneeId":"`+actorID.String()+`"}`),
			"a typo must not read as 'unassign'")
	})

	t.Run("an empty object is refused", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, put(t, `{}`))
	})
}
