package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

// resolveAnalysisToolCall lets the AnalysisChat itself resolve a paused
// tool-call approval, without needing admin access to Settings -> MCP
// Servers -- same underlying MCPToolService calls that panel's
// PendingApprovalRow already makes (see mcp_servers.go). Shared by
// AlertHandlers and IncidentHandlers (both call this with contextType
// "alert"/"incident" from their own approve/reject wrappers) -- gated here
// by confirming the call actually belongs to the alert/incident named by
// the request's "id" URL param before touching it, so admin access to one
// alert/incident's chat can never be used to approve/reject a tool call
// queued against a different one.
func resolveAnalysisToolCall(w http.ResponseWriter, r *http.Request, mcpTools *service.MCPToolService, contextType string, approve bool) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+contextType+" id")
		return
	}
	callID, err := strconv.ParseInt(chi.URLParam(r, "callId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tool call id")
		return
	}

	call, err := mcpTools.GetToolCall(r.Context(), tenantID, callID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if call == nil || call.ContextType != contextType || call.ContextID != id {
		writeError(w, http.StatusNotFound, "tool call not found for this "+contextType)
		return
	}

	if approve {
		err = mcpTools.ApproveToolCall(r.Context(), tenantID, callID, userID)
	} else {
		err = mcpTools.RejectToolCall(r.Context(), tenantID, callID, userID)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
