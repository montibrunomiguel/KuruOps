package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/service"
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
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	servers, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

type saveMCPServerRequest struct {
	Name               string   `json:"name"`
	Transport          string   `json:"transport"`
	EndpointOrCommand  string   `json:"endpointOrCommand"`
	AllowedTools       []string `json:"allowedTools"`
	EnabledFor         []string `json:"enabledFor"`
	SideEffectingTools []string `json:"sideEffectingTools"`
	// AllowAllTools is a pointer so "not sent" (leave as is on update) differs
	// from an explicit false.
	AllowAllTools *bool `json:"allowAllTools"`

	// Authentication: AuthType selects which of the other fields apply (see
	// service.MCPServerAuthInput). Every secret here is write-only -- stored in
	// secrets.Store and never returned. On update, omit AuthType to leave
	// authentication untouched.
	AuthType          string `json:"authType"`
	APIKeyHeader      string `json:"apiKeyHeader"`
	APIKey            string `json:"apiKey"`
	BearerToken       string `json:"bearerToken"`
	OAuthTokenURL     string `json:"oauthTokenUrl"`
	OAuthClientID     string `json:"oauthClientId"`
	OAuthClientSecret string `json:"oauthClientSecret"`

	// LegacyAuthToken is the pre-auth-types field. Silently ignoring it would
	// save a server the client believes is authenticated with no credential at
	// all, so it is rejected loudly instead of being mapped or dropped.
	LegacyAuthToken string `json:"authToken"`
}

func (req saveMCPServerRequest) toInput() service.MCPServerSaveInput {
	return service.MCPServerSaveInput{
		Name: req.Name, Transport: req.Transport, EndpointOrCommand: req.EndpointOrCommand,
		AllowedTools: req.AllowedTools, EnabledFor: req.EnabledFor, SideEffectingTools: req.SideEffectingTools,
		AllowAllTools: req.AllowAllTools,
		Auth: service.MCPServerAuthInput{
			Type: req.AuthType, APIKeyHeader: req.APIKeyHeader, APIKey: req.APIKey, BearerToken: req.BearerToken,
			OAuthTokenURL: req.OAuthTokenURL, OAuthClientID: req.OAuthClientID, OAuthClientSecret: req.OAuthClientSecret,
		},
	}
}

const legacyAuthTokenMessage = `"authToken" was replaced: send "authType": "bearer" with "bearerToken" (or another authType)`

func (h *MCPServerHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req saveMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.EndpointOrCommand == "" {
		writeError(w, http.StatusBadRequest, "name and endpointOrCommand are required")
		return
	}
	if req.LegacyAuthToken != "" {
		writeError(w, http.StatusBadRequest, legacyAuthTokenMessage)
		return
	}

	srv, err := h.svc.Create(r.Context(), tenantID, userID, req.toInput())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, srv)
}

func (h *MCPServerHandlers) update(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var req saveMCPServerRequest
	id, ok := decodeAndParseID(w, r, "mcp server", &req)
	if !ok {
		return
	}

	if req.LegacyAuthToken != "" {
		writeError(w, http.StatusBadRequest, legacyAuthTokenMessage)
		return
	}

	srv, err := h.svc.Update(r.Context(), tenantID, userID, id, req.toInput())
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
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mcp server id")
		return
	}
	if err := h.svc.SetEnabled(r.Context(), tenantID, userID, id, enabled); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MCPServerHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mcp server id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, userID, id); err != nil {
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
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
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
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	calls, err := h.toolSvc.PendingApprovals(r.Context(), tenantID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, calls)
}

func (h *MCPServerHandlers) approveToolCall(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
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
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
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
