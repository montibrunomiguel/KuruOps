package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

// OnCallShiftHandlers backs Settings -> On-Call Schedule -- admin-only (see
// router.go's admin group), unlike the read-only /tags catalog which any
// authenticated user can see.
type OnCallShiftHandlers struct {
	svc *service.OnCallShiftService
}

func NewOnCallShiftHandlers(svc *service.OnCallShiftService) *OnCallShiftHandlers {
	return &OnCallShiftHandlers{svc: svc}
}

func (h *OnCallShiftHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Delete("/{id}", h.delete)
	r.Put("/timezone", h.setTimezone)
}

type onCallScheduleResponse struct {
	Timezone string               `json:"timezone"`
	Shifts   []domain.OnCallShift `json:"shifts"`
}

func (h *OnCallShiftHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	shifts, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tz, err := h.svc.GetTimezone(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, onCallScheduleResponse{Timezone: tz, Shifts: shifts})
}

type createOnCallShiftRequest struct {
	UserID      uuid.UUID `json:"userId"`
	Weekday     int       `json:"weekday"`
	StartMinute int       `json:"startMinute"`
	EndMinute   int       `json:"endMinute"`
}

func (h *OnCallShiftHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())

	var req createOnCallShiftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	shift, err := h.svc.Create(r.Context(), tenantID, req.UserID, req.Weekday, req.StartMinute, req.EndMinute)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, shift)
}

func (h *OnCallShiftHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid shift id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setTimezoneRequest struct {
	Timezone string `json:"timezone"`
}

func (h *OnCallShiftHandlers) setTimezone(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())

	var req setTimezoneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.SetTimezone(r.Context(), tenantID, req.Timezone); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
