package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/service"
)

// resolveAnalysisToolCall lets the AnalysisChat itself resolve a paused
// tool-call approval, without needing admin access to Settings -> MCP
// Servers -- same underlying MCPToolService calls that panel's
// PendingApprovalRow already makes (see mcp_servers.go). Shared by
// AlertHandlers and IncidentHandlers (both call this with contextType
// "alert"/"incident" from their own approve/reject wrappers).
//
// Two gates, both required:
//   - visible() confirms the caller may actually see the alert/incident this
//     tool call is queued against, under their own allowedTags. Approving a
//     tool call EXECUTES it (a real side-effecting MCP action, see
//     MCPToolService.ApproveToolCall), so without this a tag-restricted
//     analyst who learned an out-of-scope alert's ID could trigger
//     automation against it -- strictly worse than the read-only leak the
//     same gap allowed on the comments/IOCs endpoints.
//   - the call must belong to the alert/incident named by the request's
//     "id" URL param, so access to one alert's chat can never resolve a
//     tool call queued against a different one.
func resolveAnalysisToolCall(w http.ResponseWriter, r *http.Request, mcpTools *service.MCPToolService, contextType string, approve bool, visible func(id uuid.UUID) (bool, error)) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
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

	ok, err = visible(id)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, contextType+" not found")
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
