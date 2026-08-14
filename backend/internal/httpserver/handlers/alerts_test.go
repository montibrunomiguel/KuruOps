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

func newAlertHandlerFixture(t *testing.T) (h *handlers.AlertHandlers, tenantID uuid.UUID, actorID uuid.UUID, alertID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "analyst", nil)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)

	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc)
	incidentSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	secretStore := secrets.NewEnvStore()
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), alertRepo, incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	userSvc := service.NewUserService(pool, repository.NewUserRepository())
	h = handlers.NewAlertHandlers(alertSvc, incidentSvc, aiSvc, mcpToolSvc, userSvc)

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
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository()))
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

	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc)
	alertSvc.EnableAnalysisLookup(repository.NewAIAnalysisRunRepository())
	incidentSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), alertRepo, incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	analyzed := make(chan struct{}, 1)
	aiSvc.EnableEventPublishing(func(uuid.UUID, string, any) { analyzed <- struct{}{} })
	h := handlers.NewAlertHandlers(alertSvc, incidentSvc, aiSvc, mcpToolSvc, service.NewUserService(pool, repository.NewUserRepository()))

	alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
	}, nil, 0)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"likely benign"}}]}`))
	}))
	defer srv.Close()
	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secretStore)
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

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

	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc)
	incidentSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), alertRepo, incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	analyzed := make(chan struct{}, 1)
	aiSvc.EnableEventPublishing(func(uuid.UUID, string, any) { analyzed <- struct{}{} })
	h := handlers.NewAlertHandlers(alertSvc, incidentSvc, aiSvc, mcpToolSvc, service.NewUserService(pool, repository.NewUserRepository()))

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
	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secretStore)
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

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
		incidentSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), service.NewTagService(pool, repository.NewTagRepository()), repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
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

	t.Run("unknown alert id -- empty list, not an error", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+uuid.New().String()+"/comments", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var comments []domain.AlertComment
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &comments))
		assert.Empty(t, comments)
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
