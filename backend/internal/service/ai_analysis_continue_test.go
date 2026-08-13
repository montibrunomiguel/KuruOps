package service_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// TestAIAnalysisService_ContinueAlertAnalysis_NoRunYet confirms Continue
// behaves like Start when nothing has run yet -- a fresh run is created,
// seeded with the auto-generated prompt plus the analyst's own text as an
// immediate second turn, and GetAlertTranscript hides that seed prompt (see
// buildTranscript) so the analyst only ever sees their own message and the
// model's reply.
func TestAIAnalysisService_ContinueAlertAnalysis_NoRunYet(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store)
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository()))
	aiSvc, analyzed := newAIAnalysisService(pool, store)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Looks like a scripted scan, not targeted."}}]}`))
	}))
	defer srv.Close()
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Port scan detected", Source: "wazuh", Severity: domain.SeverityMedium, Payload: testPayload,
	})
	require.NoError(t, err)

	transcript, err := aiSvc.GetAlertTranscript(t.Context(), tenantID, alert.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, transcript.Messages, "no run yet -- transcript starts empty")

	require.NoError(t, aiSvc.ContinueAlertAnalysis(t.Context(), tenantID, alert.ID, actorID, nil, "is this worth escalating?"))
	waitAnalyzed(t, analyzed)

	run := latestRun(t, pool, tenantID, "alert", alert.ID)
	require.NotNil(t, run)
	assert.Equal(t, domain.AIAnalysisRunCompleted, run.Status)

	transcript, err = aiSvc.GetAlertTranscript(t.Context(), tenantID, alert.ID, nil)
	require.NoError(t, err)
	require.Len(t, transcript.Messages, 2, "the auto-generated seed prompt is stripped -- only the analyst's text and the model's reply remain")
	assert.Equal(t, "user", transcript.Messages[0].Role)
	assert.Equal(t, "is this worth escalating?", transcript.Messages[0].Content)
	assert.Equal(t, "assistant", transcript.Messages[1].Role)
	assert.Equal(t, "Looks like a scripted scan, not targeted.", transcript.Messages[1].Content)
}

// TestAIAnalysisService_ContinueAlertAnalysis_AppendsToCompletedRun confirms
// a second Continue call against an already-completed run keeps the whole
// conversation -- it doesn't start over.
func TestAIAnalysisService_ContinueAlertAnalysis_AppendsToCompletedRun(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store)
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository()))
	aiSvc, analyzed := newAIAnalysisService(pool, store)

	reply := "first reply"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + reply + `"}}]}`))
	}))
	defer srv.Close()
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Port scan detected", Source: "wazuh", Severity: domain.SeverityMedium, Payload: testPayload,
	})
	require.NoError(t, err)

	require.NoError(t, aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, &actorID, nil))
	waitAnalyzed(t, analyzed)
	firstRun := latestRun(t, pool, tenantID, "alert", alert.ID)
	require.Equal(t, domain.AIAnalysisRunCompleted, firstRun.Status)

	reply = "second reply, building on the first"
	require.NoError(t, aiSvc.ContinueAlertAnalysis(t.Context(), tenantID, alert.ID, actorID, nil, "what about the source IP?"))
	waitAnalyzed(t, analyzed)

	secondRun := latestRun(t, pool, tenantID, "alert", alert.ID)
	require.NotNil(t, secondRun)
	assert.Equal(t, firstRun.ID, secondRun.ID, "continuing a completed run updates it in place, doesn't create a second row")
	assert.Equal(t, domain.AIAnalysisRunCompleted, secondRun.Status)

	transcript, err := aiSvc.GetAlertTranscript(t.Context(), tenantID, alert.ID, nil)
	require.NoError(t, err)
	require.Len(t, transcript.Messages, 3)
	assert.Equal(t, "first reply", transcript.Messages[0].Content)
	assert.Equal(t, "what about the source IP?", transcript.Messages[1].Content)
	assert.Equal(t, "second reply, building on the first", transcript.Messages[2].Content)
}

// TestAIAnalysisService_ContinueAlertAnalysis_RejectsWhileRunningOrPaused
// confirms Continue never drives two loop instances against the same run --
// same rule Start*Analysis already enforces via checkNotAlreadyRunning.
func TestAIAnalysisService_ContinueAlertAnalysis_RejectsWhileRunningOrPaused(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store)
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository()))
	aiSvc, analyzed := newAIAnalysisService(pool, store)

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"..."}}]}`))
	}))
	defer slow.Close()
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Slow Provider", Kind: "openai_compatible", BaseURL: &slow.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Port scan detected", Source: "wazuh", Severity: domain.SeverityMedium, Payload: testPayload,
	})
	require.NoError(t, err)

	require.NoError(t, aiSvc.StartAlertAnalysis(t.Context(), tenantID, alert.ID, &actorID, nil))
	err = aiSvc.ContinueAlertAnalysis(t.Context(), tenantID, alert.ID, actorID, nil, "hurry up?")
	assert.ErrorIs(t, err, service.ErrAnalysisInProgress)

	waitAnalyzed(t, analyzed) // drain before the next test reuses this fixture's alert/tenant
}

// TestAIAnalysisService_ContinueAlertAnalysis_LLMFailureMarksRunFailed
// confirms a broken LLM response during Continue's fresh-run driveAgentLoop
// call lands the run as 'failed' with the error recorded (see failRun),
// same as a Start*Analysis failure would.
func TestAIAnalysisService_ContinueAlertAnalysis_LLMFailureMarksRunFailed(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store)
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository()))
	aiSvc, analyzed := newAIAnalysisService(pool, store)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Broken Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Port scan detected", Source: "wazuh", Severity: domain.SeverityMedium, Payload: testPayload,
	})
	require.NoError(t, err)

	require.NoError(t, aiSvc.ContinueAlertAnalysis(t.Context(), tenantID, alert.ID, actorID, nil, "what's going on here?"))
	waitAnalyzed(t, analyzed)

	run := latestRun(t, pool, tenantID, "alert", alert.ID)
	require.NotNil(t, run)
	assert.Equal(t, domain.AIAnalysisRunFailed, run.Status)
	require.NotNil(t, run.Error)
}

// TestAIAnalysisService_ContinueIncidentAnalysis_NoRunYet is
// ContinueAlertAnalysis_NoRunYet's incident-side counterpart -- brief,
// since the two share continueRun and only differ in which entity/prompt
// they load.
func TestAIAnalysisService_ContinueIncidentAnalysis_NoRunYet(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), service.NewTagService(pool, repository.NewTagRepository()), repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	aiSvc, analyzed := newAIAnalysisService(pool, store)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recommend isolating the host"}}]}`))
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

	require.NoError(t, aiSvc.ContinueIncidentAnalysis(t.Context(), tenantID, inc.ID, actorID, nil, "what's the blast radius?"))
	waitAnalyzed(t, analyzed)

	transcript, err := aiSvc.GetIncidentTranscript(t.Context(), tenantID, inc.ID, nil)
	require.NoError(t, err)
	require.Len(t, transcript.Messages, 2)
	assert.Equal(t, "what's the blast radius?", transcript.Messages[0].Content)
	assert.Equal(t, "recommend isolating the host", transcript.Messages[1].Content)
}

// TestAIAnalysisService_PublishesOneTurnEventPerLoopIteration confirms
// driveAgentLoop fires "ai_analysis_turn" exactly once per turn -- twice for
// the non-side-effecting-tool scenario (one turn proposing the tool call,
// one turn answering after the result comes back), followed by exactly one
// final "alert" event once the run is fully committed.
func TestAIAnalysisService_PublishesOneTurnEventPerLoopIteration(t *testing.T) {
	fx := newAgenticFixture(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	mcpSrv := fakeMCPServer(t, "lookup_ip", `{"reputation":"clean"}`)
	defer mcpSrv.Close()
	llmSrv := fakeLLMServer(t, "lookup_ip", "IP reputation is clean.")
	defer llmSrv.Close()

	_, err := fx.mcp.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "Threat Intel", Transport: "http", EndpointOrCommand: mcpSrv.URL,
		AllowedTools: []string{"lookup_ip"}, EnabledFor: []string{"alert_analysis"},
	})
	require.NoError(t, err)

	provider, err := fx.llm.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &llmSrv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, fx.llm.SetDefault(t.Context(), tenantID, provider.ID))

	alertSvc := service.NewAlertService(fx.pool, repository.NewAlertRepository(), service.NewTagService(fx.pool, repository.NewTagRepository()))
	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Port scan detected", Source: "wazuh", Severity: domain.SeverityMedium, Payload: testPayload,
	})
	require.NoError(t, err)

	require.NoError(t, fx.ai.StartAlertAnalysis(t.Context(), tenantID, alert.ID, &actorID, nil))

	var eventTypes []string
	deadline := time.After(2 * time.Second)
collect:
	for {
		select {
		case eventType := <-fx.analyzed:
			eventTypes = append(eventTypes, eventType)
			if eventType != "ai_analysis_turn" {
				break collect
			}
		case <-deadline:
			t.Fatal("background analysis did not complete in time")
		}
	}

	require.Len(t, eventTypes, 3, "two per-turn events (propose the tool, then answer) plus the final completion event")
	assert.Equal(t, []string{"ai_analysis_turn", "ai_analysis_turn", "alert"}, eventTypes)
}
