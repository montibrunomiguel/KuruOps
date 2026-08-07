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
	r.Delete("/", h.delete)
}

func (h *StorageConfigHandlers) get(w http.ResponseWriter, r *http.Request) {
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

type saveS3ConfigRequest struct {
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

func (h *StorageConfigHandlers) saveS3(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())

	var req saveS3ConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.SaveS3(r.Context(), tenantID, service.SaveS3Input{
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
	tenantID, _ := middleware.TenantID(r.Context())

	var req saveGCSConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.SaveGCS(r.Context(), tenantID, service.SaveGCSInput{
		Bucket: req.Bucket, ProjectID: req.ProjectID, CredentialsJSON: req.CredentialsJSON,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *StorageConfigHandlers) delete(w http.ResponseWriter, r *http.Request) {
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
