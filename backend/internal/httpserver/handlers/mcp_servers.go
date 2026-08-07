package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

type MCPServerHandlers struct {
	svc     *service.MCPServerService
	toolSvc *service.MCPToolService
}

func NewMCPServerHandlers(svc *service.MCPServerService, toolSvc *service.MCPToolService) *MCPServerHandlers {
	return &MCPServerHandlers{svc: svc, toolSvc: toolSvc}
}

func (h *MCPServerHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Put("/{id}", h.update)
	r.Post("/{id}/enable", h.enable)
	r.Post("/{id}/disable", h.disable)
	r.Delete("/{id}", h.delete)
	r.Post("/{id}/discover-tools", h.discoverTools)

	r.Get("/tool-calls", h.listPendingToolCalls)
	r.Post("/tool-calls/{callId}/approve", h.approveToolCall)
	r.Post("/tool-calls/{callId}/reject", h.rejectToolCall)
}

func (h *MCPServerHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	servers, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

type saveMCPServerRequest struct {
	Name               string   `json:"name"`
	Transport          string   `json:"transport"`
	EndpointOrCommand  string   `json:"endpointOrCommand"`
	AuthToken          string   `json:"authToken"`
	AllowedTools       []string `json:"allowedTools"`
	EnabledFor         []string `json:"enabledFor"`
	SideEffectingTools []string `json:"sideEffectingTools"`
}

func (h *MCPServerHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())

	var req saveMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.EndpointOrCommand == "" {
		writeError(w, http.StatusBadRequest, "name and endpointOrCommand are required")
		return
	}

	srv, err := h.svc.Create(r.Context(), tenantID, userID, service.MCPServerSaveInput{
		Name: req.Name, Transport: req.Transport, EndpointOrCommand: req.EndpointOrCommand,
		AuthToken: req.AuthToken, AllowedTools: req.AllowedTools, EnabledFor: req.EnabledFor,
		SideEffectingTools: req.SideEffectingTools,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, srv)
}

func (h *MCPServerHandlers) update(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mcp server id")
		return
	}

	var req saveMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	srv, err := h.svc.Update(r.Context(), tenantID, id, service.MCPServerSaveInput{
		Name: req.Name, Transport: req.Transport, EndpointOrCommand: req.EndpointOrCommand,
		AuthToken: req.AuthToken, AllowedTools: req.AllowedTools, EnabledFor: req.EnabledFor,
		SideEffectingTools: req.SideEffectingTools,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *MCPServerHandlers) enable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, true)
}

func (h *MCPServerHandlers) disable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, false)
}

func (h *MCPServerHandlers) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mcp server id")
		return
	}
	if err := h.svc.SetEnabled(r.Context(), tenantID, id, enabled); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MCPServerHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mcp server id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// discoverTools connects to the registered server live and returns its full
// tool catalog via MCP tools/list — what the "Discover tools" button in
// Settings -> MCP Servers calls, so an admin picks allowedTools/
// sideEffectingTools from what the server actually exposes instead of
// typing tool names from memory.
func (h *MCPServerHandlers) discoverTools(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mcp server id")
		return
	}

	tools, err := h.toolSvc.DiscoverTools(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tools)
}

func (h *MCPServerHandlers) listPendingToolCalls(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	calls, err := h.toolSvc.PendingApprovals(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, calls)
}

func (h *MCPServerHandlers) approveToolCall(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	callID, err := strconv.ParseInt(chi.URLParam(r, "callId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tool call id")
		return
	}
	if err := h.toolSvc.ApproveToolCall(r.Context(), tenantID, callID, userID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MCPServerHandlers) rejectToolCall(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	callID, err := strconv.ParseInt(chi.URLParam(r, "callId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tool call id")
		return
	}
	if err := h.toolSvc.RejectToolCall(r.Context(), tenantID, callID, userID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
