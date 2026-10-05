package service_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// runAllowAllAnalysis drives one "Analisar com IA" run against an allow-all MCP
// server exposing toolsJSON, with an LLM that immediately asks for toolName.
func runAllowAllAnalysis(t *testing.T, toolsJSON, toolName string) (fx agenticFixture, tenantID, actorID uuid.UUID, run domain.AIAnalysisRun) {
	t.Helper()
	fx = newAgenticFixture(t)
	tid := testutil.NewTenant(t)
	aid := testutil.NewUser(t, tid, "admin", nil)

	mcpSrv := newToolServer(t, toolsJSON)
	llmSrv := fakeLLMServer(t, toolName, "Done.")
	defer llmSrv.Close()

	// No AllowedTools at all: the tool is reachable only because the server is
	// allow-all.
	_, err := fx.mcp.Create(t.Context(), tid, aid, service.MCPServerSaveInput{
		Name: "EDR", Transport: "http", EndpointOrCommand: mcpSrv.URL,
		AllowAllTools: boolPtr(true), EnabledFor: []string{"alert_analysis"},
	})
	require.NoError(t, err)

	provider, err := fx.llm.Create(t.Context(), tid, aid, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &llmSrv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, fx.llm.SetDefault(t.Context(), tid, aid, provider.ID))

	alertSvc := service.NewAlertService(fx.pool, repository.NewAlertRepository(), service.NewTagService(fx.pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository()), repository.NewPlaybookRepository())
	alert, _, err := alertSvc.Ingest(t.Context(), tid, testutil.NewWebhookEndpoint(t, tid), domain.Alert{
		Title: "Suspicious process", Source: "edr", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
	}, nil, 0)
	require.NoError(t, err)

	require.NoError(t, fx.ai.StartAlertAnalysis(t.Context(), tid, alert.ID, &aid, nil))
	fx.waitAnalyzed(t)

	tx := testutil.BeginTx(t, fx.pool, tid)
	runs, err := fx.runs.ListByContext(t.Context(), tx, "alert", alert.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	return fx, tid, aid, runs[0]
}

func TestAIAnalysisService_AgenticLoop_AllowAll_ReadOnlyToolRunsWithoutAnAllowList(t *testing.T) {
	_, _, _, run := runAllowAllAnalysis(t, `[{"name":"lookup_ip","annotations":{"readOnlyHint":true}}]`, "lookup_ip")

	assert.Equal(t, domain.AIAnalysisRunCompleted, run.Status, "a tool the server declares read-only needs no approval")
}

func TestAIAnalysisService_AgenticLoop_AllowAll_UnannotatedToolPausesForApproval(t *testing.T) {
	fx, tenantID, _, run := runAllowAllAnalysis(t, `[{"name":"quarantine_host"}]`, "quarantine_host")

	assert.Equal(t, domain.AIAnalysisRunPaused, run.Status, "use-all-tools must not mean an unknown tool acts on its own")
	pending, err := fx.mcpTool.PendingApprovals(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "quarantine_host", pending[0].ToolName)
}
