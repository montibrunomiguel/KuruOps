package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

// IdentityConfigHandlers is Settings -> Identity Providers: where an admin
// registers the tenant's own LDAP directory and/or SAML IdP. Distinct from
// AuthHandlers, which serves the unauthenticated login flows that consume
// this configuration.
type IdentityConfigHandlers struct {
	svc *service.IdentityConfigService
}

func NewIdentityConfigHandlers(svc *service.IdentityConfigService) *IdentityConfigHandlers {
	return &IdentityConfigHandlers{svc: svc}
}

func (h *IdentityConfigHandlers) Routes(r chi.Router) {
	r.Get("/ldap", h.getLDAP)
	r.Put("/ldap", h.saveLDAP)
	r.Get("/saml", h.getSAML)
	r.Put("/saml", h.saveSAML)
}

func (h *IdentityConfigHandlers) getLDAP(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	cfg, err := h.svc.GetLDAPConfig(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

type saveLDAPConfigRequest struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	UseTLS         bool   `json:"useTls"`
	BindDN         string `json:"bindDn"`
	BindPassword   string `json:"bindPassword"`
	UserBaseDN     string `json:"userBaseDn"`
	UserFilter     string `json:"userFilter"`
	GroupBaseDN    string `json:"groupBaseDn"`
	GroupAttribute string `json:"groupAttribute"`
}

func (h *IdentityConfigHandlers) saveLDAP(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())

	var req saveLDAPConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.SaveLDAPConfig(r.Context(), tenantID, service.SaveLDAPConfigInput{
		Host: req.Host, Port: req.Port, UseTLS: req.UseTLS, BindDN: req.BindDN,
		BindPassword: req.BindPassword, UserBaseDN: req.UserBaseDN, UserFilter: req.UserFilter,
		GroupBaseDN: req.GroupBaseDN, GroupAttribute: req.GroupAttribute,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *IdentityConfigHandlers) getSAML(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	cfg, err := h.svc.GetSAMLConfig(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

type saveSAMLConfigRequest struct {
	IDPMetadataURL *string `json:"idpMetadataUrl,omitempty"`
	IDPMetadataXML *string `json:"idpMetadataXml,omitempty"`
	ACSURL         string  `json:"acsUrl"`
	SPEntityID     string  `json:"spEntityId"`
	GroupAttribute *string `json:"groupAttribute,omitempty"`
}

func (h *IdentityConfigHandlers) saveSAML(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())

	var req saveSAMLConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.SaveSAMLConfig(r.Context(), tenantID, service.SaveSAMLConfigInput{
		IDPMetadataURL: req.IDPMetadataURL, IDPMetadataXML: req.IDPMetadataXML,
		ACSURL: req.ACSURL, SPEntityID: req.SPEntityID, GroupAttribute: req.GroupAttribute,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
