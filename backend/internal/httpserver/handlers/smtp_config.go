package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

// SMTPConfigHandlers is Settings -> SMTP: admin-only, same shape as
// StorageConfigHandlers.
type SMTPConfigHandlers struct {
	svc *service.SMTPConfigService
}

func NewSMTPConfigHandlers(svc *service.SMTPConfigService) *SMTPConfigHandlers {
	return &SMTPConfigHandlers{svc: svc}
}

func (h *SMTPConfigHandlers) Routes(r chi.Router) {
	r.Get("/", h.get)
	r.Put("/", h.save)
	r.Delete("/", h.delete)
	r.Post("/test", h.sendTest)
}

func (h *SMTPConfigHandlers) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	cfg, err := h.svc.Get(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

type saveSMTPConfigRequest struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	UseTLS      bool   `json:"useTls"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	FromAddress string `json:"fromAddress"`
	FromName    string `json:"fromName"`
}

func (h *SMTPConfigHandlers) save(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())

	var req saveSMTPConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.Save(r.Context(), tenantID, service.SaveSMTPInput{
		Host: req.Host, Port: req.Port, UseTLS: req.UseTLS,
		Username: req.Username, Password: req.Password,
		FromAddress: req.FromAddress, FromName: req.FromName,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SMTPConfigHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sendTestEmailRequest struct {
	To string `json:"to"`
}

func (h *SMTPConfigHandlers) sendTest(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}

	var req sendTestEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.To == "" {
		writeError(w, http.StatusBadRequest, "to is required")
		return
	}

	if err := h.svc.SendTestEmail(r.Context(), tenantID, req.To); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
