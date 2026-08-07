package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// agenticFixture wires an AIAnalysisService with the onToolCallResolved
// callback actually connected (see cmd/api/main.go for the same wiring) --
// newAIAnalysisService in ai_analysis_service_test.go deliberately doesn't,
// since none of those tests register an MCP server that could pause.
type agenticFixture struct {
	pool    *db.Pool
	ai      *service.AIAnalysisService
	mcpTool *service.MCPToolService
	mcp     *service.MCPServerService
	llm     *service.LLMProviderService
	runs    *repository.AIAnalysisRunRepository
}

func newAgenticFixture(t *testing.T) agenticFixture {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	store := secrets.NewEnvStore()

	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, store)
	mcpSvc := service.NewMCPServerService(pool, mcpServerRepo, store)
	runsRepo := repository.NewAIAnalysisRunRepository()
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), repository.NewAlertRepository(), repository.NewIncidentRepository(), store,
		mcpServerRepo, mcpToolSvc, runsRepo, aiToolCallRepo,
	)
	mcpToolSvc.SetOnToolCallResolved(aiSvc.ResumeAnalysisRun)

	return agenticFixture{
		pool: pool, ai: aiSvc, mcpTool: mcpToolSvc, mcp: mcpSvc,
		llm: service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store), runs: runsRepo,
	}
}

// mcpRPCEnvelope mirrors internal/mcpclient/jsonrpc_test.go's fixture shape
// for a fake MCP server.
type mcpRPCEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// fakeMCPServer serves initialize/tools/list/tools/call for a single tool
// named toolName -- CallTool just echoes back callResult verbatim.
func fakeMCPServer(t *testing.T, toolName, callResult string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpRPCEnvelope
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": "2025-03-26"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"tools": []map[string]any{{"name": toolName, "description": "test tool", "inputSchema": map[string]any{"type": "object"}}},
				},
			})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"content": []map[string]string{{"type": "text", "text": callResult}}},
			})
		default:
			t.Fatalf("unexpected mcp method %q", req.Method)
		}
	}))
}

// fakeLLMServer serves an OpenAI-compatible /chat/completions: as long as
// the incoming conversation has no "tool" role message yet, it requests
// toolName; once a tool result is present, it answers with finalText and no
// further tool calls.
func fakeLLMServer(t *testing.T, toolName, finalText string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		hasToolResult := false
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasToolResult = true
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if hasToolResult {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": finalText}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"role": "assistant",
				"tool_calls": []map[string]any{{
					"id": "call_1", "type": "function",
					"function": map[string]any{"name": toolName, "arguments": `{"host":"10.0.0.5"}`},
				}},
			}}},
		})
	}))
}

func TestAIAnalysisService_AgenticLoop_NonSideEffectingTool(t *testing.T) {
	fx := newAgenticFixture(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	mcpSrv := fakeMCPServer(t, "lookup_ip", `{"reputation":"clean"}`)
	defer mcpSrv.Close()
	llmSrv := fakeLLMServer(t, "lookup_ip", "IP reputation is clean, likely a false positive.")
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
		Title: "Port scan detected", Source: "wazuh", Severity: domain.SeverityMedium, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	result, err := fx.ai.AnalyzeAlert(t.Context(), tenantID, alert.ID, actorID, nil)
	require.NoError(t, err)
	assert.Equal(t, "IP reputation is clean, likely a false positive.", result)

	tx := testutil.BeginTx(t, fx.pool, tenantID)
	runs, err := fx.runs.ListByContext(t.Context(), tx, "alert", alert.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, domain.AIAnalysisRunCompleted, runs[0].Status)
	require.NotNil(t, runs[0].Result)
	assert.Equal(t, "IP reputation is clean, likely a false positive.", *runs[0].Result)
}

func TestAIAnalysisService_AgenticLoop_SideEffectingTool_PausesAndResumesOnApproval(t *testing.T) {
	fx := newAgenticFixture(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	mcpSrv := fakeMCPServer(t, "quarantine_host", `{"quarantined":true}`)
	defer mcpSrv.Close()
	llmSrv := fakeLLMServer(t, "quarantine_host", "Host quarantined, incident contained.")
	defer llmSrv.Close()

	_, err := fx.mcp.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "EDR", Transport: "http", EndpointOrCommand: mcpSrv.URL,
		AllowedTools: []string{"quarantine_host"}, SideEffectingTools: []string{"quarantine_host"},
		EnabledFor: []string{"alert_analysis"},
	})
	require.NoError(t, err)

	provider, err := fx.llm.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &llmSrv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, fx.llm.SetDefault(t.Context(), tenantID, provider.ID))

	alertSvc := service.NewAlertService(fx.pool, repository.NewAlertRepository(), service.NewTagService(fx.pool, repository.NewTagRepository()))
	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Ransomware behavior detected", Source: "edr", Severity: domain.SeverityCritical, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	t.Run("first call pauses instead of returning a final answer", func(t *testing.T) {
		result, err := fx.ai.AnalyzeAlert(t.Context(), tenantID, alert.ID, actorID, nil)
		require.NoError(t, err)
		assert.Contains(t, result, "requires analyst approval")
		assert.True(t, strings.Contains(result, "alert"), "the paused message should reference the context type")

		tx := testutil.BeginTx(t, fx.pool, tenantID)
		runs, err := fx.runs.ListByContext(t.Context(), tx, "alert", alert.ID)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		assert.Equal(t, domain.AIAnalysisRunPaused, runs[0].Status)
		require.NotNil(t, runs[0].PendingToolCallID)
	})

	t.Run("approving the tool call resumes the run to completion", func(t *testing.T) {
		pending, err := fx.mcpTool.PendingApprovals(t.Context(), tenantID)
		require.NoError(t, err)
		require.Len(t, pending, 1)
		assert.Equal(t, "quarantine_host", pending[0].ToolName)

		require.NoError(t, fx.mcpTool.ApproveToolCall(t.Context(), tenantID, pending[0].ID, actorID))

		tx := testutil.BeginTx(t, fx.pool, tenantID)
		runs, err := fx.runs.ListByContext(t.Context(), tx, "alert", alert.ID)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		assert.Equal(t, domain.AIAnalysisRunCompleted, runs[0].Status)
		require.NotNil(t, runs[0].Result)
		assert.Equal(t, "Host quarantined, incident contained.", *runs[0].Result)

		pending, err = fx.mcpTool.PendingApprovals(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Empty(t, pending)
	})
}

func TestAIAnalysisService_AgenticLoop_RejectedToolCall(t *testing.T) {
	fx := newAgenticFixture(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	mcpSrv := fakeMCPServer(t, "quarantine_host", `{"quarantined":true}`)
	defer mcpSrv.Close()
	llmSrv := fakeLLMServer(t, "quarantine_host", "Proceeding without quarantine; monitoring only.")
	defer llmSrv.Close()

	_, err := fx.mcp.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "EDR", Transport: "http", EndpointOrCommand: mcpSrv.URL,
		AllowedTools: []string{"quarantine_host"}, SideEffectingTools: []string{"quarantine_host"},
		EnabledFor: []string{"alert_analysis"},
	})
	require.NoError(t, err)

	provider, err := fx.llm.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &llmSrv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, fx.llm.SetDefault(t.Context(), tenantID, provider.ID))

	alertSvc := service.NewAlertService(fx.pool, repository.NewAlertRepository(), service.NewTagService(fx.pool, repository.NewTagRepository()))
	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Suspicious activity", Source: "edr", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	_, err = fx.ai.AnalyzeAlert(t.Context(), tenantID, alert.ID, actorID, nil)
	require.NoError(t, err)

	pending, err := fx.mcpTool.PendingApprovals(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, pending, 1)

	require.NoError(t, fx.mcpTool.RejectToolCall(t.Context(), tenantID, pending[0].ID, actorID))

	tx := testutil.BeginTx(t, fx.pool, tenantID)
	runs, err := fx.runs.ListByContext(t.Context(), tx, "alert", alert.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, domain.AIAnalysisRunCompleted, runs[0].Status, "a rejected tool call still lets the model produce a final answer, just without that tool's result")
	require.NotNil(t, runs[0].Result)
	assert.Equal(t, "Proceeding without quarantine; monitoring only.", *runs[0].Result)
}
