package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

// StorageConfigHandlers is Settings -> Storage Integration: admin-only,
// same shape as IdentityConfigHandlers' LDAP/SAML routes.
type StorageConfigHandlers struct {
	svc *service.StorageConfigService
}

func NewStorageConfigHandlers(svc *service.StorageConfigService) *StorageConfigHandlers {
	return &StorageConfigHandlers{svc: svc}
}

func (h *StorageConfigHandlers) Routes(r chi.Router) {
	r.Get("/", h.get)
	r.Put("/s3", h.saveS3)
	r.Put("/gcs", h.saveGCS)
	r.Put("/gdrive/service-account", h.saveGDriveServiceAccount)
	r.Get("/gdrive/oauth/authorize-url", h.gdriveAuthorizeURL)
	r.Delete("/", h.delete)
}

func (h *StorageConfigHandlers) get(w http.ResponseWriter, r *http.Request) {
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

type saveS3ConfigRequest struct {
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

func (h *StorageConfigHandlers) saveS3(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req saveS3ConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.SaveS3(r.Context(), tenantID, actorID, service.SaveS3Input{
		Bucket: req.Bucket, Region: req.Region, AccessKeyID: req.AccessKeyID, SecretAccessKey: req.SecretAccessKey,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type saveGCSConfigRequest struct {
	Bucket          string `json:"bucket"`
	ProjectID       string `json:"projectId"`
	CredentialsJSON string `json:"credentialsJson"`
}

func (h *StorageConfigHandlers) saveGCS(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req saveGCSConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.SaveGCS(r.Context(), tenantID, actorID, service.SaveGCSInput{
		Bucket: req.Bucket, ProjectID: req.ProjectID, CredentialsJSON: req.CredentialsJSON,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type saveGDriveServiceAccountRequest struct {
	FolderID           string `json:"folderId"`
	ServiceAccountJSON string `json:"serviceAccountJson"`
}

func (h *StorageConfigHandlers) saveGDriveServiceAccount(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req saveGDriveServiceAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.SaveGDriveServiceAccount(r.Context(), tenantID, actorID, service.SaveGDriveServiceAccountInput{
		FolderID: req.FolderID, ServiceAccountJSON: req.ServiceAccountJSON,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// gdriveAuthorizeURL is the admin-initiated, already-authenticated first
// step of the OAuth flow -- returns {url} for the frontend to redirect
// the browser to. The unauthenticated second half (Google's callback)
// lives in OAuthCallbackHandlers, mounted separately outside /api/v1 --
// see that handler's doc comment for why.
func (h *StorageConfigHandlers) gdriveAuthorizeURL(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	folderID := r.URL.Query().Get("folderId")
	url, err := h.svc.GetGDriveAuthorizeURL(r.Context(), tenantID, userID, folderID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (h *StorageConfigHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, actorID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
