package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// TestAlertHandlers_AnalysisChat_ApproveToolCall_AlreadyResolved and its
// reject-side sibling below cover resolveAnalysisToolCall's third failure
// mode end-to-end through the HTTP handler -- TestAlertHandlers_ResolveAnalysisToolCall
// (alerts_test.go) already covers "bad call id" (400) and "unknown call id"
// (404), and the ownership-scoping tests in analysis_chat_test.go cover
// "belongs to a different alert" (404), but nothing previously exercised
// MCPToolService.ApproveToolCall/RejectToolCall's own
// `c.Status != domain.ToolCallProposed` guard through this handler -- only
// at the service layer (mcp_tool_service_test.go). A tool call already
// resolved once (approved or rejected) must refuse a second resolution
// attempt with 400, not silently re-apply it or 500.
func TestAlertHandlers_AnalysisChat_ApproveToolCall_AlreadyResolved(t *testing.T) {
	fx := newChatFixture(t, "irrelevant")
	r := newRouter(fx.alertHandlers.Routes)
	pool := testutil.RequireTestDB(t)

	mcpSrv := fakeMCPToolServer(t, "quarantine_host", `{"quarantined":true}`)
	defer mcpSrv.Close()
	mcpSvc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
	server, err := mcpSvc.Create(t.Context(), fx.tenantID, fx.actorID, service.MCPServerSaveInput{
		Name: "EDR", Transport: "http", EndpointOrCommand: mcpSrv.URL,
		AllowedTools: []string{"quarantine_host"}, SideEffectingTools: []string{"quarantine_host"},
		EnabledFor: []string{"alert_analysis"},
	})
	require.NoError(t, err)

	call, err := fx.mcpTool.ProposeToolCall(t.Context(), fx.tenantID, server.ID, "alert", fx.alertID, "quarantine_host", map[string]any{"host": "10.0.0.5"})
	require.NoError(t, err)
	callID := strconv.FormatInt(call.ID, 10)

	firstApprove := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.alertID.String()+"/analyze/tool-calls/"+callID+"/approve", nil), fx.tenantID, fx.actorID, nil))
	require.Equal(t, http.StatusNoContent, firstApprove.Code)

	t.Run("approving an already-approved call again -- 400", func(t *testing.T) {
		rec := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.alertID.String()+"/analyze/tool-calls/"+callID+"/approve", nil), fx.tenantID, fx.actorID, nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "not awaiting approval")
	})

	t.Run("rejecting an already-approved call -- 400, does not flip it back", func(t *testing.T) {
		rec := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.alertID.String()+"/analyze/tool-calls/"+callID+"/reject", nil), fx.tenantID, fx.actorID, nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "not awaiting approval")
	})
}

func TestAlertHandlers_AnalysisChat_RejectToolCall_AlreadyResolved(t *testing.T) {
	fx := newChatFixture(t, "irrelevant")
	r := newRouter(fx.alertHandlers.Routes)
	pool := testutil.RequireTestDB(t)

	mcpSvc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
	server, err := mcpSvc.Create(t.Context(), fx.tenantID, fx.actorID, service.MCPServerSaveInput{
		Name: "EDR", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AllowedTools: []string{"quarantine_host"}, SideEffectingTools: []string{"quarantine_host"},
		EnabledFor: []string{"alert_analysis"},
	})
	require.NoError(t, err)

	call, err := fx.mcpTool.ProposeToolCall(t.Context(), fx.tenantID, server.ID, "alert", fx.alertID, "quarantine_host", map[string]any{"host": "10.0.0.5"})
	require.NoError(t, err)
	callID := strconv.FormatInt(call.ID, 10)

	firstReject := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.alertID.String()+"/analyze/tool-calls/"+callID+"/reject", nil), fx.tenantID, fx.actorID, nil))
	require.Equal(t, http.StatusNoContent, firstReject.Code)

	rec := doRequest(r, withClaims(httptest.NewRequest("POST", "/"+fx.alertID.String()+"/analyze/tool-calls/"+callID+"/reject", nil), fx.tenantID, fx.actorID, nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "not awaiting approval")
}
