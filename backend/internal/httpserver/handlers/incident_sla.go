package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/service"
)

// IncidentSLAHandlers is Settings -> Incident SLAs: admin-only.
type IncidentSLAHandlers struct {
	svc *service.IncidentSLAService
}

func NewIncidentSLAHandlers(svc *service.IncidentSLAService) *IncidentSLAHandlers {
	return &IncidentSLAHandlers{svc: svc}
}

func (h *IncidentSLAHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Put("/", h.save)
	r.Delete("/{id}", h.delete)
}

func (h *IncidentSLAHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	policies, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, policies)
}

type saveIncidentSLARequest struct {
	Severity         domain.Severity         `json:"severity"`
	Priority         domain.IncidentPriority `json:"priority"`
	DueWithinMinutes int                     `json:"dueWithinMinutes"`
}

func (h *IncidentSLAHandlers) save(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}

	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req saveIncidentSLARequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	policy, err := h.svc.Save(r.Context(), tenantID, actorID, req.Severity, req.Priority, req.DueWithinMinutes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (h *IncidentSLAHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid policy id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, actorID, id); err != nil {
		writeInternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
