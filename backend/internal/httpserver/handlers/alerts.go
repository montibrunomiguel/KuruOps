package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
)

// maxBulkIDs caps a single bulk request -- generous enough to cover a full
// page at the list pages' largest page size (100/page, see Pagination.tsx),
// with headroom, while still bounding how many single-ID ChangeStatus/
// ChangePhase transactions one request can trigger.
const maxBulkIDs = 200

type AlertHandlers struct {
	svc *service.AlertService
	ai  *service.AIAnalysisService
	// mcpTools backs the AnalysisChat's inline tool-call approve/reject --
	// same underlying service Settings -> MCP Servers' pending-approvals
	// panel already calls, just also reachable from here so an analyst
	// without admin access can resolve a call from inside the chat itself.
	mcpTools *service.MCPToolService
	// users resolves the acting user's display name for addComment -- same
	// reasoning as IncidentHandlers.users (see domain.AlertComment.AuthorName).
	users *service.UserService
}

func NewAlertHandlers(svc *service.AlertService, ai *service.AIAnalysisService, mcpTools *service.MCPToolService, users *service.UserService) *AlertHandlers {
	return &AlertHandlers{svc: svc, ai: ai, mcpTools: mcpTools, users: users}
}

func (h *AlertHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Get("/{id}", h.get)
	r.Post("/bulk/status", h.bulkChangeStatus)
	r.Post("/bulk/close", h.bulkClose)
	r.Post("/{id}/status", h.changeStatus)
	r.Post("/{id}/close", h.close)
	r.Put("/{id}/tags", h.updateTags)
	r.Put("/{id}/severity", h.overrideSeverity)
	r.Put("/{id}/assignee", h.reassign)
	r.Get("/{id}/alerts", h.listLinkedAlerts)
	r.Put("/{id}/alerts/{otherId}", h.linkAlert)
	r.Delete("/{id}/alerts/{otherId}", h.unlinkAlert)
	r.Post("/{id}/escalate", h.escalate)
	r.Post("/{id}/analyze", h.analyze)
	r.Get("/{id}/analyze/messages", h.getAnalysisChat)
	r.Post("/{id}/analyze/messages", h.continueAnalysisChat)
	r.Post("/{id}/analyze/tool-calls/{callId}/approve", h.approveAnalysisToolCall)
	r.Post("/{id}/analyze/tool-calls/{callId}/reject", h.rejectAnalysisToolCall)
	r.Get("/{id}/comments", h.listComments)
	r.Post("/{id}/comments", h.addComment)
}

func (h *AlertHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}

	f := repository.ListAlertsFilter{}
	if v := r.URL.Query().Get("severity"); v != "" {
		s := domain.Severity(v)
		f.Severity = &s
	}
	if v := r.URL.Query().Get("status"); v != "" {
		s := domain.AlertStatus(v)
		f.Status = &s
	}
	if v := r.URL.Query().Get("source"); v != "" {
		f.Source = &v
	}
	if v := r.URL.Query().Get("tag"); v != "" {
		f.Tag = &v
	}
	if v := r.URL.Query().Get("correlated"); v != "" {
		b := v == "true"
		f.Correlated = &b
	}
	// Trimmed, not just non-empty: plainto_tsquery('english', '   ') has no
	// extractable lexemes and matches nothing, so a whitespace-only query
	// (e.g. an accidental space-bar press) would otherwise silently zero
	// out the list instead of behaving like "no search applied," the way
	// clearing the search box entirely does.
	if v := strings.TrimSpace(r.URL.Query().Get("q")); v != "" {
		f.Q = &v
	}
	f.ReceivedSince = parseSince(r)
	f.ReceivedUntil = parseUntil(r)
	f.Limit, f.Offset = parsePaging(r)
	f.AllowedTags = middleware.AllowedTags(r.Context())

	alerts, err := h.svc.List(r.Context(), tenantID, f)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}

	total, err := h.svc.Count(r.Context(), tenantID, f)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, alerts)
}

func (h *AlertHandlers) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	alert, err := h.svc.Get(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if alert == nil {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}
	writeJSON(w, http.StatusOK, alert)
}

type changeStatusRequest struct {
	Status domain.AlertStatus `json:"status"`
}

func (h *AlertHandlers) changeStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req changeStatusRequest
	id, ok := decodeAndParseID(w, r, "alert", &req)
	if !ok {
		return
	}

	if err := h.svc.ChangeStatus(r.Context(), tenantID, id, userID, req.Status, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type bulkChangeStatusRequest struct {
	IDs    []uuid.UUID        `json:"ids"`
	Status domain.AlertStatus `json:"status"`
}

// bulkChangeStatus applies one status change across many alerts (the
// AlertsListPage row-selection toolbar's Apply button). Unlike changeStatus,
// a per-alert failure doesn't fail the whole request -- the 200 response
// carries one result per id, same as AlertService.BulkChangeStatus itself
// returns, so the frontend can show which of the selected alerts succeeded.
func (h *AlertHandlers) bulkChangeStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req bulkChangeStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids must not be empty")
		return
	}
	if len(req.IDs) > maxBulkIDs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d ids per request", maxBulkIDs))
		return
	}

	results := h.svc.BulkChangeStatus(r.Context(), tenantID, userID, req.IDs, req.Status, middleware.AllowedTags(r.Context()))
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

type bulkCloseRequest struct {
	IDs            []uuid.UUID           `json:"ids"`
	Classification domain.Classification `json:"classification"`
	Comment        string                `json:"comment"`
}

// bulkClose closes several alerts under one classification -- see
// AlertService.BulkClose for why closing needs its own bulk route rather
// than riding on bulk/status, and why no attachment is accepted. Answers
// 200 with per-alert results even when some fail, same contract as
// bulk/status.
func (h *AlertHandlers) bulkClose(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req bulkCloseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids must not be empty")
		return
	}
	if len(req.IDs) > maxBulkIDs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d ids per request", maxBulkIDs))
		return
	}
	// Checked here rather than per-alert inside the loop: an unusable
	// classification is a bad request, not a hundred individual failures.
	if !domain.ClassificationIsValid(req.Classification) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid classification %q (accepted: %s)",
			req.Classification, domain.ClassificationNames()))
		return
	}

	results := h.svc.BulkClose(r.Context(), tenantID, userID, req.IDs,
		domain.CloseAlertInput{Classification: req.Classification, Comment: req.Comment},
		middleware.AllowedTags(r.Context()))
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

type closeAlertRequest struct {
	Classification domain.Classification `json:"classification"`
	Comment        string                `json:"comment"`
	AttachmentURL  *string               `json:"attachmentUrl,omitempty"`
}

func (h *AlertHandlers) close(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req closeAlertRequest
	id, ok := decodeAndParseID(w, r, "alert", &req)
	if !ok {
		return
	}

	in := domain.CloseAlertInput{
		Classification: req.Classification,
		Comment:        req.Comment,
		AttachmentURL:  req.AttachmentURL,
	}
	if err := h.svc.Close(r.Context(), tenantID, id, userID, in, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateTagsRequest struct {
	Tags []string `json:"tags"`
}

func (h *AlertHandlers) updateTags(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req updateTagsRequest
	id, ok := decodeAndParseID(w, r, "alert", &req)
	if !ok {
		return
	}

	if err := h.svc.UpdateTags(r.Context(), tenantID, id, userID, req.Tags, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type overrideSeverityRequest struct {
	Severity domain.Severity `json:"severity"`
}

func (h *AlertHandlers) overrideSeverity(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req overrideSeverityRequest
	id, ok := decodeAndParseID(w, r, "alert", &req)
	if !ok {
		return
	}

	if err := h.svc.OverrideSeverity(r.Context(), tenantID, id, userID, req.Severity, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type reassignAlertRequest struct {
	AnalystID *uuid.UUID `json:"analystId"`
	// hasAnalystIDKey records whether the key was present at all, which the
	// pointer alone cannot express -- absent and explicit-null both decode
	// to nil. Set by UnmarshalJSON below.
	hasAnalystIDKey bool
}

// UnmarshalJSON decodes the request while recording whether "analystId" was
// actually supplied, so the handler can tell "unassign" from "wrong field
// name". Decoding into an alias avoids recursing back into this method.
func (r *reassignAlertRequest) UnmarshalJSON(data []byte) error {
	type plain reassignAlertRequest
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	*r = reassignAlertRequest(p)
	_, r.hasAnalystIDKey = keys["analystId"]
	return nil
}

func (h *AlertHandlers) reassign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req reassignAlertRequest
	id, ok := decodeAndParseID(w, r, "alert", &req)
	if !ok {
		return
	}
	// AnalystID is a pointer so that an explicit null can mean "unassign".
	// That made a body with no recognised field indistinguishable from a
	// deliberate unassign: a client sending the wrong key name got a 204
	// and a silently cleared assignee. Requiring the key to be present
	// keeps the explicit-null unassign while turning a typo into an error.
	if !req.hasAnalystIDKey {
		writeError(w, http.StatusBadRequest,
			`body must contain an "analystId" field: a user id to assign, or null to unassign`)
		return
	}

	if err := h.svc.Reassign(r.Context(), tenantID, id, userID, req.AnalystID, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AlertHandlers) listLinkedAlerts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	linked, err := h.svc.LinkedAlerts(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, linked)
}

func (h *AlertHandlers) linkAlert(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}
	otherID, err := uuid.Parse(chi.URLParam(r, "otherId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid linked alert id")
		return
	}

	if err := h.svc.LinkAlert(r.Context(), tenantID, id, otherID, userID, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AlertHandlers) unlinkAlert(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}
	otherID, err := uuid.Parse(chi.URLParam(r, "otherId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid linked alert id")
		return
	}

	if err := h.svc.UnlinkAlert(r.Context(), tenantID, id, otherID, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type escalateResponse struct {
	IncidentID uuid.UUID `json:"incidentId"`
}

// escalate delegates to AlertService.Escalate -- see its doc comment for
// what actually happens (creates+links an incident, marks the alert
// escalated, fires the next manual-escalation chain step).
func (h *AlertHandlers) escalate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	allowedTags := middleware.AllowedTags(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	incident, err := h.svc.Escalate(r.Context(), tenantID, userID, id, allowedTags)
	if err != nil {
		// 409 rather than 400: the alert is fine, the request just lost a
		// race with an earlier escalation (double-click, retry after a
		// timeout). Same treatment ErrAnalysisInProgress gets below.
		if errors.Is(err, service.ErrAlreadyEscalated) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if incident == nil {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}

	writeJSON(w, http.StatusOK, escalateResponse{IncidentID: incident.ID})
}

type analyzeStartedResponse struct {
	Status string `json:"status"`
}

// analyze kicks off analysis in the background and returns immediately --
// the eventual result (or failure) shows up on the alert itself (see
// domain.Alert's LatestAnalysis* fields) once a live-update event tells a
// connected AlertDetailPage to reload (see AIAnalysisService's doc
// comment). 409 if one's already running/paused for this alert.
func (h *AlertHandlers) analyze(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	err = h.ai.StartAlertAnalysis(r.Context(), tenantID, id, &userID, middleware.AllowedTags(r.Context()))
	if err != nil {
		if errors.Is(err, service.ErrAnalysisInProgress) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, analyzeStartedResponse{Status: "running"})
}

// continueMessageRequest is the body POST .../analyze/messages takes on
// both alerts and incidents -- kept here (not duplicated in incidents.go),
// same sharing as analyzeStartedResponse above.
type continueMessageRequest struct {
	Text string `json:"text"`
}

// getAnalysisChat backs AnalysisChat's initial load and its
// refetch-on-SSE-event -- see AIAnalysisService.GetAlertTranscript.
func (h *AlertHandlers) getAnalysisChat(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}
	transcript, err := h.ai.GetAlertTranscript(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, transcript)
}

// continueAnalysisChat is what the chat's message box posts to -- kicks off
// the next turn in the background and returns immediately, same 202 shape
// as analyze (see AIAnalysisService.ContinueAlertAnalysis's doc comment for
// why the analyst's own message is already persisted by the time this
// returns, even though the LLM's reply isn't yet).
func (h *AlertHandlers) continueAnalysisChat(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req continueMessageRequest
	id, ok := decodeAndParseID(w, r, "alert", &req)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	err := h.ai.ContinueAlertAnalysis(r.Context(), tenantID, id, userID, middleware.AllowedTags(r.Context()), req.Text)
	if err != nil {
		if errors.Is(err, service.ErrAnalysisInProgress) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, analyzeStartedResponse{Status: "running"})
}

// approveAnalysisToolCall/rejectAnalysisToolCall let the chat itself resolve
// a paused tool-call approval, without needing admin access to Settings ->
// MCP Servers -- same underlying MCPToolService calls that panel's
// PendingApprovalRow already makes (see mcp_servers.go), gated here by
// confirming the call actually belongs to this alert before touching it.
func (h *AlertHandlers) approveAnalysisToolCall(w http.ResponseWriter, r *http.Request) {
	resolveAnalysisToolCall(w, r, h.mcpTools, "alert", true, h.alertVisible(r))
}

func (h *AlertHandlers) rejectAnalysisToolCall(w http.ResponseWriter, r *http.Request) {
	resolveAnalysisToolCall(w, r, h.mcpTools, "alert", false, h.alertVisible(r))
}

// alertVisible is the visibility gate resolveAnalysisToolCall applies before
// resolving a tool call -- see its doc comment for why.
func (h *AlertHandlers) alertVisible(r *http.Request) func(uuid.UUID) (bool, error) {
	return func(id uuid.UUID) (bool, error) {
		tenantID, _ := middleware.TenantID(r.Context())
		a, err := h.svc.Get(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
		return a != nil, err
	}
}

func (h *AlertHandlers) listComments(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	comments, found, err := h.svc.Comments(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}
	writeJSON(w, http.StatusOK, comments)
}

type addAlertCommentRequest struct {
	Body          string  `json:"body"`
	AttachmentURL *string `json:"attachmentUrl,omitempty"`
}

func (h *AlertHandlers) addComment(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req addAlertCommentRequest
	id, ok := decodeAndParseID(w, r, "alert", &req)
	if !ok {
		return
	}
	if req.Body == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}

	// The JWT doesn't carry the user's display name -- resolve it here so
	// the comment can denormalize AuthorName (see domain.AlertComment),
	// same reasoning as IncidentHandlers.addComment.
	actor, err := h.users.Get(r.Context(), tenantID, userID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if actor == nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	comment, err := h.svc.AddComment(r.Context(), tenantID, id, userID, actor.Name, req.Body, req.AttachmentURL, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}
