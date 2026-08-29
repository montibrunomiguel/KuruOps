package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/service"
)

// RetentionConfigHandlers is Settings -> Retention: admin-only. Just GET/PUT
// -- see RetentionConfigService.Save's doc comment for why there's no
// DELETE route here, unlike StorageConfigHandlers/SMTPConfigHandlers.
type RetentionConfigHandlers struct {
	svc *service.RetentionConfigService
}

func NewRetentionConfigHandlers(svc *service.RetentionConfigService) *RetentionConfigHandlers {
	return &RetentionConfigHandlers{svc: svc}
}

func (h *RetentionConfigHandlers) Routes(r chi.Router) {
	r.Get("/", h.get)
	r.Put("/", h.save)
}

func (h *RetentionConfigHandlers) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	cfg, err := h.svc.Get(r.Context(), tenantID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

type saveRetentionConfigRequest struct {
	AlertRetentionMonths    int `json:"alertRetentionMonths"`
	IncidentRetentionMonths int `json:"incidentRetentionMonths"`
}

func (h *RetentionConfigHandlers) save(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}

	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req saveRetentionConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.Save(r.Context(), tenantID, actorID, service.SaveRetentionInput{
		AlertRetentionMonths: req.AlertRetentionMonths, IncidentRetentionMonths: req.IncidentRetentionMonths,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
