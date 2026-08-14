package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// chatFixture wires an AlertHandlers/IncidentHandlers pair against a real
// LLM provider double -- what AnalysisChat's GET/POST /analyze/messages
// endpoints need, on top of what newAlertHandlerFixture/newIncidentHandlerFixture
// already set up (those don't configure an LLM provider, since most of
// their tests don't need one).
type chatFixture struct {
	alertHandlers    *handlers.AlertHandlers
	incidentHandlers *handlers.IncidentHandlers
	mcpTool          *service.MCPToolService
	tenantID         uuid.UUID
	actorID          uuid.UUID
	alertID          uuid.UUID
	incidentID       uuid.UUID
	analyzed         <-chan string
}

func newChatFixture(t *testing.T, reply string) chatFixture {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	secretStore := secrets.NewEnvStore()

	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc, repository.NewPlaybookRepository())
	incidentSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolSvc := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	aiSvc := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), alertRepo, incidentRepo, secretStore,
		mcpServerRepo, mcpToolSvc, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	analyzed := make(chan string, 8)
	aiSvc.EnableEventPublishing(func(_ uuid.UUID, eventType string, _ any) { analyzed <- eventType })
	userSvc := service.NewUserService(pool, repository.NewUserRepository())
	postmortemSvc := service.NewPostmortemService(incidentSvc, aiSvc)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + reply + `"}}]}`))
	}))
	t.Cleanup(srv.Close)
	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secretStore)
	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

	alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
	}, nil, 0)
	require.NoError(t, err)

	inc, err := incidentSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Ransomware suspected", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
	})
	require.NoError(t, err)

	return chatFixture{
		alertHandlers:    handlers.NewAlertHandlers(alertSvc, incidentSvc, aiSvc, mcpToolSvc, userSvc),
		incidentHandlers: handlers.NewIncidentHandlers(incidentSvc, userSvc, aiSvc, postmortemSvc, mcpToolSvc),
		mcpTool:          mcpToolSvc,
		tenantID:         tenantID, actorID: actorID, alertID: alert.ID, incidentID: inc.ID,
		analyzed: analyzed,
	}
}

func (fx chatFixture) waitAnalyzed(t *testing.T) {
	t.Helper()
	for {
		select {
		case eventType := <-fx.analyzed:
			if eventType != "ai_analysis_turn" {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("background analysis did not complete in time")
		}
	}
}

func TestAlertHandlers_AnalysisChat_GetThenContinueThenGet(t *testing.T) {
	fx := newChatFixture(t, "Looks like a routine scan.")
	r := newRouter(fx.alertHandlers.Routes)

	empty := doRequest(r, withClaims(httptest.NewRequest("GET", "/"+fx.alertID.String()+"/analyze/messages", nil), fx.tenantID, fx.actorID, nil))
	require.Equal(t, http.StatusOK, empty.Code)
	var transcript service.ChatTranscript
	require.NoError(t, json.Unmarshal(empty.Body.Bytes(), &transcript))
	assert.Empty(t, transcript.Messages, "no analysis run yet")

	body, _ := json.Marshal(map[string]string{"text": "is this worth escalating?"})
	post := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.alertID.String()+"/analyze/messages", bytes.NewReader(body)), fx.tenantID, fx.actorID, nil))
	require.Equal(t, http.StatusAccepted, post.Code)
	fx.waitAnalyzed(t)

	after := doRequest(r, withClaims(httptest.NewRequest("GET", "/"+fx.alertID.String()+"/analyze/messages", nil), fx.tenantID, fx.actorID, nil))
	require.Equal(t, http.StatusOK, after.Code)
	require.NoError(t, json.Unmarshal(after.Body.Bytes(), &transcript))
	require.Len(t, transcript.Messages, 2)
	assert.Equal(t, "user", transcript.Messages[0].Role)
	assert.Equal(t, "is this worth escalating?", transcript.Messages[0].Content)
	assert.Equal(t, "assistant", transcript.Messages[1].Role)
	assert.Equal(t, "Looks like a routine scan.", transcript.Messages[1].Content)
}

func TestAlertHandlers_AnalysisChat_ContinueRejectsEmptyText(t *testing.T) {
	fx := newChatFixture(t, "irrelevant")
	r := newRouter(fx.alertHandlers.Routes)

	body, _ := json.Marshal(map[string]string{"text": "   "})
	rec := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.alertID.String()+"/analyze/messages", bytes.NewReader(body)), fx.tenantID, fx.actorID, nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "text is required")
}

func TestIncidentHandlers_AnalysisChat_GetThenContinueThenGet(t *testing.T) {
	fx := newChatFixture(t, "Recommend isolating the affected host.")
	r := newRouter(fx.incidentHandlers.Routes)

	body, _ := json.Marshal(map[string]string{"text": "what's the blast radius?"})
	post := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.incidentID.String()+"/analyze/messages", bytes.NewReader(body)), fx.tenantID, fx.actorID, nil))
	require.Equal(t, http.StatusAccepted, post.Code)
	fx.waitAnalyzed(t)

	after := doRequest(r, withClaims(httptest.NewRequest("GET", "/"+fx.incidentID.String()+"/analyze/messages", nil), fx.tenantID, fx.actorID, nil))
	require.Equal(t, http.StatusOK, after.Code)
	var transcript service.ChatTranscript
	require.NoError(t, json.Unmarshal(after.Body.Bytes(), &transcript))
	require.Len(t, transcript.Messages, 2)
	assert.Equal(t, "what's the blast radius?", transcript.Messages[0].Content)
	assert.Equal(t, "Recommend isolating the affected host.", transcript.Messages[1].Content)
}

// TestAlertHandlers_AnalysisChat_ApproveToolCall_ScopedToOwningAlert confirms
// the inline approve/reject endpoints refuse to touch a tool call that
// belongs to a different alert (or a different context type entirely) than
// the one in the URL -- the ownership check resolveAnalysisToolCall does via
// MCPToolService.GetToolCall before ever calling ApproveToolCall/RejectToolCall.
func TestAlertHandlers_AnalysisChat_ApproveToolCall_ScopedToOwningAlert(t *testing.T) {
	fx := newChatFixture(t, "irrelevant")
	r := newRouter(fx.alertHandlers.Routes)
	pool := testutil.RequireTestDB(t)

	mcpSrv := fakeMCPToolServer(t, "quarantine_host", `{"quarantined":true}`)
	defer mcpSrv.Close()
	mcpSvc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore())
	server, err := mcpSvc.Create(t.Context(), fx.tenantID, fx.actorID, service.MCPServerSaveInput{
		Name: "EDR", Transport: "http", EndpointOrCommand: mcpSrv.URL,
		AllowedTools: []string{"quarantine_host"}, SideEffectingTools: []string{"quarantine_host"},
		EnabledFor: []string{"alert_analysis"},
	})
	require.NoError(t, err)

	// Proposed directly against the alert's context, bypassing a full
	// agentic run -- this test only needs a real proposed tool call to
	// exist, not a full LLM/MCP round trip.
	call, err := fx.mcpTool.ProposeToolCall(t.Context(), fx.tenantID, server.ID, "alert", fx.alertID, "quarantine_host", map[string]any{"host": "10.0.0.5"})
	require.NoError(t, err)
	require.Equal(t, domain.ToolCallProposed, call.Status)

	// A second, unrelated alert -- approving via *its* URL must not be able
	// to resolve the first alert's tool call.
	otherAlertID := newSecondAlert(t, fx.alertHandlers, fx.tenantID)
	callID := strconv.FormatInt(call.ID, 10)

	approveWrongAlert := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+otherAlertID.String()+"/analyze/tool-calls/"+callID+"/approve", nil), fx.tenantID, fx.actorID, nil))
	assert.Equal(t, http.StatusNotFound, approveWrongAlert.Code)

	approveOwningAlert := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.alertID.String()+"/analyze/tool-calls/"+callID+"/approve", nil), fx.tenantID, fx.actorID, nil))
	assert.Equal(t, http.StatusNoContent, approveOwningAlert.Code)
}

// TestAlertHandlers_AnalysisChat_RejectToolCall confirms the inline reject
// endpoint resolves a pending call without needing the MCP server to
// respond to anything (RejectToolCall never dials -- see
// MCPToolService.RejectToolCall).
func TestAlertHandlers_AnalysisChat_RejectToolCall(t *testing.T) {
	fx := newChatFixture(t, "irrelevant")
	r := newRouter(fx.alertHandlers.Routes)
	pool := testutil.RequireTestDB(t)

	mcpSvc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore())
	server, err := mcpSvc.Create(t.Context(), fx.tenantID, fx.actorID, service.MCPServerSaveInput{
		Name: "EDR", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AllowedTools: []string{"quarantine_host"}, SideEffectingTools: []string{"quarantine_host"},
		EnabledFor: []string{"alert_analysis"},
	})
	require.NoError(t, err)

	call, err := fx.mcpTool.ProposeToolCall(t.Context(), fx.tenantID, server.ID, "alert", fx.alertID, "quarantine_host", map[string]any{"host": "10.0.0.5"})
	require.NoError(t, err)
	callID := strconv.FormatInt(call.ID, 10)

	rec := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.alertID.String()+"/analyze/tool-calls/"+callID+"/reject", nil), fx.tenantID, fx.actorID, nil))
	assert.Equal(t, http.StatusNoContent, rec.Code)

	pending, err := fx.mcpTool.PendingApprovals(t.Context(), fx.tenantID)
	require.NoError(t, err)
	assert.Empty(t, pending)
}

// TestIncidentHandlers_AnalysisChat_ApproveToolCall_ScopedToOwningIncident
// mirrors the alert-side ownership-scoping test above.
func TestIncidentHandlers_AnalysisChat_ApproveToolCall_ScopedToOwningIncident(t *testing.T) {
	fx := newChatFixture(t, "irrelevant")
	r := newRouter(fx.incidentHandlers.Routes)
	pool := testutil.RequireTestDB(t)

	mcpSrv := fakeMCPToolServer(t, "quarantine_host", `{"quarantined":true}`)
	defer mcpSrv.Close()
	mcpSvc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore())
	server, err := mcpSvc.Create(t.Context(), fx.tenantID, fx.actorID, service.MCPServerSaveInput{
		Name: "EDR", Transport: "http", EndpointOrCommand: mcpSrv.URL,
		AllowedTools: []string{"quarantine_host"}, SideEffectingTools: []string{"quarantine_host"},
		EnabledFor: []string{"incident_analysis"},
	})
	require.NoError(t, err)

	call, err := fx.mcpTool.ProposeToolCall(t.Context(), fx.tenantID, server.ID, "incident", fx.incidentID, "quarantine_host", map[string]any{"host": "10.0.0.5"})
	require.NoError(t, err)
	callID := strconv.FormatInt(call.ID, 10)

	rejectCall, err := fx.mcpTool.ProposeToolCall(t.Context(), fx.tenantID, server.ID, "incident", fx.incidentID, "quarantine_host", map[string]any{"host": "10.0.0.6"})
	require.NoError(t, err)
	rejectCallID := strconv.FormatInt(rejectCall.ID, 10)

	notFound := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+uuid.NewString()+"/analyze/tool-calls/"+callID+"/approve", nil), fx.tenantID, fx.actorID, nil))
	assert.Equal(t, http.StatusNotFound, notFound.Code)

	approve := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.incidentID.String()+"/analyze/tool-calls/"+callID+"/approve", nil), fx.tenantID, fx.actorID, nil))
	assert.Equal(t, http.StatusNoContent, approve.Code)

	reject := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.incidentID.String()+"/analyze/tool-calls/"+rejectCallID+"/reject", nil), fx.tenantID, fx.actorID, nil))
	assert.Equal(t, http.StatusNoContent, reject.Code)
}

// fakeMCPToolServer serves just enough of the MCP JSON-RPC protocol
// (initialize + tools/call) for MCPToolService.ApproveToolCall's execute()
// step to succeed -- mirrors internal/service/ai_analysis_agentic_test.go's
// fakeMCPServer, duplicated here rather than exported since it's a test-only
// double and the two packages don't otherwise share test helpers.
func fakeMCPToolServer(t *testing.T, toolName, callResult string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": "2025-03-26"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
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
