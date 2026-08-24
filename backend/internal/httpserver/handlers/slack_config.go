package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

// SlackConfigHandlers is Settings -> Conectores -> Slack: admin-only,
// connect/disconnect a workspace. Same shape as StorageConfigHandlers'
// gdrive OAuth routes -- gdriveAuthorizeURL's doc comment explains the
// admin-initiated-vs-callback split this mirrors.
type SlackConfigHandlers struct {
	svc *service.SlackConfigService
}

func NewSlackConfigHandlers(svc *service.SlackConfigService) *SlackConfigHandlers {
	return &SlackConfigHandlers{svc: svc}
}

func (h *SlackConfigHandlers) Routes(r chi.Router) {
	r.Get("/", h.get)
	r.Get("/oauth/authorize-url", h.authorizeURL)
	r.Delete("/", h.disconnect)
}

func (h *SlackConfigHandlers) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	cfg, err := h.svc.Get(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *SlackConfigHandlers) authorizeURL(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	url, err := h.svc.GetAuthorizeURL(r.Context(), tenantID, userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (h *SlackConfigHandlers) disconnect(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	if err := h.svc.Disconnect(r.Context(), tenantID, actorID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
