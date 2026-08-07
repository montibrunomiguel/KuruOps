package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// newAIAnalysisService wires an AIAnalysisService with no MCP servers
// registered -- every test in this file exercises the pre-agentic-loop,
// single-LLM-call path (resolveAgentTools finds nothing to offer). The
// agentic loop itself (tool proposal, pause-for-approval, resume) is
// covered in ai_analysis_agentic_test.go against a real MCP server fixture.
func newAIAnalysisService(pool *db.Pool, store secrets.Store) *service.AIAnalysisService {
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, store)
	return service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), repository.NewAlertRepository(), repository.NewIncidentRepository(), store,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
}

func TestAIAnalysisService_AnalyzeAlert(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store)
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository()))
	aiSvc := newAIAnalysisService(pool, store)

	t.Run("no provider configured", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
		})
		require.NoError(t, err)

		_, err = aiSvc.AnalyzeAlert(t.Context(), tenantID, alert.ID, actorID, nil)
		assert.ErrorContains(t, err, "no LLM provider configured")
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"likely a brute-force login attempt"}}]}`))
	}))
	defer srv.Close()

	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

	t.Run("analyzes the alert and logs an ai_analysis_run event", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
		})
		require.NoError(t, err)

		result, err := aiSvc.AnalyzeAlert(t.Context(), tenantID, alert.ID, actorID, nil)
		require.NoError(t, err)
		assert.Equal(t, "likely a brute-force login attempt", result)
	})

	t.Run("an out-of-scope alert reads as not found", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload,
		})
		require.NoError(t, err)

		_, err = aiSvc.AnalyzeAlert(t.Context(), tenantID, alert.ID, actorID, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}

func TestAIAnalysisService_AnalyzeIncident(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), service.NewTagService(pool, repository.NewTagRepository()), repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	aiSvc := newAIAnalysisService(pool, store)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recommend immediate containment"}}]}`))
	}))
	defer srv.Close()

	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Ransomware suspected", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
	})
	require.NoError(t, err)

	result, err := aiSvc.AnalyzeIncident(t.Context(), tenantID, inc.ID, actorID, nil)
	require.NoError(t, err)
	assert.Equal(t, "recommend immediate containment", result)

	t.Run("an unknown incident id fails", func(t *testing.T) {
		_, err := aiSvc.AnalyzeIncident(t.Context(), tenantID, inc.ID, actorID, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}
