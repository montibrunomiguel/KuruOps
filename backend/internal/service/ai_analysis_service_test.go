package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
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
//
// StartAlertAnalysis/StartIncidentAnalysis only block for the quick
// synchronous validation -- the actual LLM call runs in its own goroutine
// (see AIAnalysisService's doc comment), so a test asserting on the
// eventual result needs to wait for it to finish first; wiring
// EnableEventPublishing here gives tests exactly that signal (the same
// live-update event a real Broadcaster would fan out to SSE clients).
func newAIAnalysisService(pool *db.Pool, store secrets.Store) (*service.AIAnalysisService, <-chan string) {
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, store)
	svc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), repository.NewAlertRepository(), repository.NewIncidentRepository(), store,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	analyzed := make(chan string, 8)
	svc.EnableEventPublishing(func(_ uuid.UUID, eventType string, _ any) {
		analyzed <- eventType
	})
	return svc, analyzed
}

// waitAnalyzed blocks until the background analysis started by
// StartAlertAnalysis/StartIncidentAnalysis/Continue*Analysis fires its
// final "alert"/"incident" completion event, draining and ignoring any
// "ai_analysis_turn" events seen first (Continue*Analysis always drives its
// turns through driveAgentLoop, which fires one of those per turn even
// with no MCP tools configured -- see AIAnalysisService.publishTurn). Fails
// the test after 2s -- generous for a test LLM double that responds
// instantly, tight enough to fail fast if notifyAnalyzed regresses.
func waitAnalyzed(t *testing.T, analyzed <-chan string) {
	t.Helper()
	for {
		select {
		case eventType := <-analyzed:
			if eventType != "ai_analysis_turn" {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("background analysis did not complete in time")
		}
	}
}

// latestRun fetches the most recent analysis run for contextID directly
// from the repository -- what tests use to inspect the outcome of a
// background analysis, now that Start*Analysis has nothing to return.
func latestRun(t *testing.T, pool *db.Pool, tenantID uuid.UUID, contextType string, contextID uuid.UUID) *domain.AIAnalysisRun {
	t.Helper()
	runs := repository.NewAIAnalysisRunRepository()
	tx := testutil.BeginTx(t, pool, tenantID)
	run, err := runs.LatestRun(t.Context(), tx, contextType, contextID)
	require.NoError(t, err)
	return run
}

func TestAIAnalysisService_StartAlertAnalysis(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store, repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository()), repository.NewPlaybookRepository())
	aiSvc, analyzed := newAIAnalysisService(pool, store)

	t.Run("no provider configured", func(t *testing.T) {
		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
		}, nil, 0)
		require.NoError(t, err)

		err = aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, &actorID, nil)
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
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, actorID, provider.ID))

	t.Run("analyzes the alert in the background and logs an ai_analysis_run event", func(t *testing.T) {
		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
		}, nil, 0)
		require.NoError(t, err)

		require.NoError(t, aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, &actorID, nil))
		waitAnalyzed(t, analyzed)

		run := latestRun(t, pool, tenantID, "alert", alert.ID)
		require.NotNil(t, run)
		assert.Equal(t, domain.AIAnalysisRunCompleted, run.Status)
		require.NotNil(t, run.Result)
		assert.Equal(t, "likely a brute-force login attempt", *run.Result)
	})

	t.Run("an out-of-scope alert reads as not found", func(t *testing.T) {
		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload,
		}, nil, 0)
		require.NoError(t, err)

		err = aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, &actorID, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("a second start while one is already running is rejected", func(t *testing.T) {
		// A slow LLM double this time -- long enough that the first
		// analysis is still 'running' when the second StartAlertAnalysis
		// call's synchronous checkNotAlreadyRunning check runs.
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(300 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"..."}}]}`))
		}))
		defer slow.Close()
		slowProvider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "Slow Provider", Kind: "openai_compatible", BaseURL: &slow.URL, Model: "gpt-4o", APIKey: "sk-test",
		})
		require.NoError(t, err)
		require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, actorID, slowProvider.ID))
		defer func() {
			require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, actorID, provider.ID))
		}()

		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
		}, nil, 0)
		require.NoError(t, err)

		require.NoError(t, aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, &actorID, nil))
		err = aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, &actorID, nil)
		assert.ErrorIs(t, err, service.ErrAnalysisInProgress)

		waitAnalyzed(t, analyzed) // drain the first (slow) analysis's completion before the next subtest
	})
}

// TestAIAnalysisService_AutoTriggerGating covers the AutoAnalyzeAllAlerts
// gate in buildClient: actorID nil (the unattended ingest-time trigger, see
// AlertService.EnableAutoAnalysis) must respect the tenant's default
// provider's opt-in, while an explicit analyst call (actorID non-nil) must
// never be affected by it -- "Analyze with AI" always works regardless of
// this setting.
func TestAIAnalysisService_AutoTriggerGating(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store, repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository()), repository.NewPlaybookRepository())
	aiSvc, analyzed := newAIAnalysisService(pool, store)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"analyzed"}}]}`))
	}))
	defer srv.Close()

	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, actorID, provider.ID))

	t.Run("auto-trigger (actorID nil) is rejected when the default provider hasn't opted in", func(t *testing.T) {
		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
		}, nil, 0)
		require.NoError(t, err)

		err = aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, nil, nil)
		assert.ErrorIs(t, err, service.ErrAutoAnalysisDisabled)
	})

	t.Run("an explicit analyst call (actorID set) is unaffected by the opt-in", func(t *testing.T) {
		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
		}, nil, 0)
		require.NoError(t, err)

		require.NoError(t, aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, &actorID, nil))
		waitAnalyzed(t, analyzed)
	})

	t.Run("auto-trigger proceeds once the default provider opts in", func(t *testing.T) {
		_, err := llmSvc.Update(t.Context(), tenantID, actorID, provider.ID, service.LLMProviderSaveInput{
			Name: provider.Name, Kind: provider.Kind, BaseURL: provider.BaseURL, Model: provider.Model,
			AutoAnalyzeAllAlerts: true,
		})
		require.NoError(t, err)

		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
			Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
		}, nil, 0)
		require.NoError(t, err)

		require.NoError(t, aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, nil, nil))
		waitAnalyzed(t, analyzed)

		run := latestRun(t, pool, tenantID, "alert", alert.ID)
		require.NotNil(t, run)
		assert.Equal(t, domain.AIAnalysisRunCompleted, run.Status)
	})
}

func TestAIAnalysisService_StartIncidentAnalysis(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store, repository.NewAdminAuditEventRepository())
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository()), repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	aiSvc, analyzed := newAIAnalysisService(pool, store)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recommend immediate containment"}}]}`))
	}))
	defer srv.Close()

	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, actorID, provider.ID))

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Ransomware suspected", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
	})
	require.NoError(t, err)

	require.NoError(t, aiSvc.StartIncidentAnalysis(t.Context(), tenantID, inc.ID, &actorID, nil))
	waitAnalyzed(t, analyzed)

	run := latestRun(t, pool, tenantID, "incident", inc.ID)
	require.NotNil(t, run)
	assert.Equal(t, domain.AIAnalysisRunCompleted, run.Status)
	require.NotNil(t, run.Result)
	assert.Equal(t, "recommend immediate containment", *run.Result)

	t.Run("an out-of-scope incident reads as not found", func(t *testing.T) {
		err := aiSvc.StartIncidentAnalysis(t.Context(), tenantID, inc.ID, &actorID, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}
