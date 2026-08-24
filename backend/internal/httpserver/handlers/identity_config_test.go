package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestIdentityConfigHandlers_LDAP(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	h := handlers.NewIdentityConfigHandlers(service.NewIdentityConfigService(pool, repository.NewIdentityConfigRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository()))
	r := newRouter(h.Routes)

	t.Run("get before any config -- 200 with null body", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/ldap", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "null\n", rec.Body.String())
	})

	t.Run("save without a bind password -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"host": "ldap.example.com"})
		req := withClaims(httptest.NewRequest("PUT", "/ldap", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/ldap", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]any{"host": "ldap.example.com", "port": 636, "bindDn": "cn=svc", "bindPassword": "s3cret"})
	req := withClaims(httptest.NewRequest("PUT", "/ldap", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	t.Run("get after save", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/ldap", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.NotEqual(t, "null\n", rec.Body.String())
	})

	t.Run("delete removes the config", func(t *testing.T) {
		delReq := withClaims(httptest.NewRequest("DELETE", "/ldap", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, delReq).Code)

		req := withClaims(httptest.NewRequest("GET", "/ldap", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, "null\n", rec.Body.String())
	})
}

func TestIdentityConfigHandlers_SAML(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	h := handlers.NewIdentityConfigHandlers(service.NewIdentityConfigService(pool, repository.NewIdentityConfigRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository()))
	r := newRouter(h.Routes)

	t.Run("get before any config", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/saml", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "null\n", rec.Body.String())
	})

	t.Run("save invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/saml", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]string{
		"spEntityId":     "https://argusops.example/saml/metadata",
		"acsUrl":         "https://argusops.example/auth/saml/acs",
		"idpMetadataUrl": "https://idp.example.com/metadata",
	})
	req := withClaims(httptest.NewRequest("PUT", "/saml", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	t.Run("get after save", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/saml", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.NotEqual(t, "null\n", rec.Body.String())
	})

	t.Run("delete removes the config", func(t *testing.T) {
		delReq := withClaims(httptest.NewRequest("DELETE", "/saml", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, delReq).Code)

		req := withClaims(httptest.NewRequest("GET", "/saml", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, "null\n", rec.Body.String())
	})
}

func TestIdentityConfigHandlers_MissingTenantContext(t *testing.T) {
	h := handlers.NewIdentityConfigHandlers(nil)
	r := newRouter(h.Routes)

	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/ldap"}, {"DELETE", "/ldap"}, {"GET", "/saml"}, {"DELETE", "/saml"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
		})
	}
}
