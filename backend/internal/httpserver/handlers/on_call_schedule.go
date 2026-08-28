package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/service"
)

// OnCallScheduleHandlers backs Settings -> On-Call Schedule -- admin-only
// (see router.go's admin group), unlike the read-only /tags catalog which
// any authenticated user can see.
type OnCallScheduleHandlers struct {
	svc *service.OnCallScheduleService
}

func NewOnCallScheduleHandlers(svc *service.OnCallScheduleService) *OnCallScheduleHandlers {
	return &OnCallScheduleHandlers{svc: svc}
}

func (h *OnCallScheduleHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	r.Post("/{id}/default", h.setDefault)
	r.Put("/timezone", h.setTimezone)
	r.Post("/{id}/overrides", h.createOverride)
	r.Delete("/{id}/overrides/{overrideId}", h.deleteOverride)
}

func (h *OnCallScheduleHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	schedules, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, schedules)
}

func (h *OnCallScheduleHandlers) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid schedule id")
		return
	}
	sched, err := h.svc.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sched == nil {
		writeError(w, http.StatusNotFound, "schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

type workingHoursIntervalRequest struct {
	Weekdays    []int `json:"weekdays"`
	StartMinute int   `json:"startMinute"`
	EndMinute   int   `json:"endMinute"`
}

type saveOnCallScheduleRequest struct {
	Name             string                        `json:"name"`
	ParticipantIDs   []uuid.UUID                   `json:"participantIds"`
	HandoverAt       time.Time                     `json:"handoverAt"`
	PeriodDays       int                           `json:"periodDays"`
	ConcurrentShifts int                           `json:"concurrentShifts"`
	WorkingHoursMode domain.OnCallWorkingHoursMode `json:"workingHoursMode"`
	WorkingHours     []workingHoursIntervalRequest `json:"workingHours"`
}

func (req saveOnCallScheduleRequest) toInput() domain.SaveOnCallScheduleInput {
	in := domain.SaveOnCallScheduleInput{
		Name:             req.Name,
		ParticipantIDs:   req.ParticipantIDs,
		HandoverAt:       req.HandoverAt,
		PeriodDays:       req.PeriodDays,
		ConcurrentShifts: req.ConcurrentShifts,
		WorkingHoursMode: req.WorkingHoursMode,
	}
	for _, iv := range req.WorkingHours {
		in.WorkingHours = append(in.WorkingHours, domain.SaveWorkingHoursInput{
			Weekdays: iv.Weekdays, StartMinute: iv.StartMinute, EndMinute: iv.EndMinute,
		})
	}
	return in
}

func (h *OnCallScheduleHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req saveOnCallScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sched, err := h.svc.Create(r.Context(), tenantID, actorID, req.toInput())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sched)
}

func (h *OnCallScheduleHandlers) update(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var req saveOnCallScheduleRequest
	id, ok := decodeAndParseID(w, r, "schedule", &req)
	if !ok {
		return
	}

	sched, err := h.svc.Update(r.Context(), tenantID, actorID, id, req.toInput())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

func (h *OnCallScheduleHandlers) delete(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadRequest, "invalid schedule id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, actorID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *OnCallScheduleHandlers) setDefault(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadRequest, "invalid schedule id")
		return
	}
	if err := h.svc.SetDefault(r.Context(), tenantID, actorID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setTimezoneRequest struct {
	Timezone string `json:"timezone"`
}

func (h *OnCallScheduleHandlers) setTimezone(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req setTimezoneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.SetTimezone(r.Context(), tenantID, actorID, req.Timezone); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type createOverrideRequest struct {
	UserID uuid.UUID `json:"userId"`
	Date   string    `json:"date"`
}

func (h *OnCallScheduleHandlers) createOverride(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	scheduleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid schedule id")
		return
	}

	var req createOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	override, err := h.svc.CreateOverride(r.Context(), tenantID, scheduleID, userID, req.UserID, req.Date)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, override)
}

func (h *OnCallScheduleHandlers) deleteOverride(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "overrideId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid override id")
		return
	}
	if err := h.svc.DeleteOverride(r.Context(), tenantID, actorID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
