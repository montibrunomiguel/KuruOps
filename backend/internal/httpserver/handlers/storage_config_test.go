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

func TestStorageConfigHandlers_S3(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir(), nil, "", "", "", repository.NewAdminAuditEventRepository())
	h := handlers.NewStorageConfigHandlers(svc)
	r := newRouter(h.Routes)

	t.Run("get before any config -- 200 with null body", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "null\n", rec.Body.String())
	})

	t.Run("save without a secret access key -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"bucket": "evidence", "region": "us-east-1", "accessKeyId": "AKIA"})
		req := withClaims(httptest.NewRequest("PUT", "/s3", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/s3", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	saveBody, _ := json.Marshal(map[string]string{
		"bucket": "evidence", "region": "us-east-1", "accessKeyId": "AKIA", "secretAccessKey": "s3cret",
	})
	saveReq := withClaims(httptest.NewRequest("PUT", "/s3", bytes.NewReader(saveBody)), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, saveReq).Code)

	t.Run("get after save reflects the config but never the secret", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), "s3cret")
		assert.Contains(t, rec.Body.String(), `"provider":"s3"`)
	})

	t.Run("delete turns the integration off", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		getReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		getRec := doRequest(r, getReq)
		assert.Equal(t, "null\n", getRec.Body.String())
	})
}

func TestStorageConfigHandlers_GCS(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir(), nil, "", "", "", repository.NewAdminAuditEventRepository())
	h := handlers.NewStorageConfigHandlers(svc)
	r := newRouter(h.Routes)

	t.Run("save without a project id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"bucket": "evidence"})
		req := withClaims(httptest.NewRequest("PUT", "/gcs", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save without credentials on initial configuration -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"bucket": "evidence", "projectId": "my-project"})
		req := withClaims(httptest.NewRequest("PUT", "/gcs", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/gcs", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]string{
		"bucket": "evidence", "projectId": "my-project", "credentialsJson": `{"type":"service_account"}`,
	})
	req := withClaims(httptest.NewRequest("PUT", "/gcs", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	getReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	getRec := doRequest(r, getReq)
	assert.NotContains(t, getRec.Body.String(), "service_account")
	assert.Contains(t, getRec.Body.String(), `"provider":"gcs"`)
}

func TestStorageConfigHandlers_GDriveServiceAccount(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir(), nil, "", "", "", repository.NewAdminAuditEventRepository())
	h := handlers.NewStorageConfigHandlers(svc)
	r := newRouter(h.Routes)

	t.Run("save without a folder id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"serviceAccountJson": `{"type":"service_account"}`})
		req := withClaims(httptest.NewRequest("PUT", "/gdrive/service-account", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save without credentials on initial configuration -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"folderId": "folder-1"})
		req := withClaims(httptest.NewRequest("PUT", "/gdrive/service-account", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]string{
		"folderId": "folder-1", "serviceAccountJson": `{"type":"service_account","private_key":"top-secret-key-material"}`,
	})
	req := withClaims(httptest.NewRequest("PUT", "/gdrive/service-account", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	getReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	getRec := doRequest(r, getReq)
	assert.NotContains(t, getRec.Body.String(), "top-secret-key-material", "the plaintext credentials JSON never lands in the GET response")
	assert.Contains(t, getRec.Body.String(), `"provider":"gdrive"`)
	assert.Contains(t, getRec.Body.String(), `"gdriveAuthMethod":"service_account"`)
}

func TestStorageConfigHandlers_GDriveAuthorizeURL(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	// oauth_states.user_id is a real FK to users(id) (the installing
	// admin's identity is meaningful, not a throwaway) -- unlike most
	// handler tests here, this one can't get away with a random uuid.New().
	userID := testutil.NewUser(t, tenantID, "admin", nil)

	t.Run("no Google OAuth client configured -- 400", func(t *testing.T) {
		svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir(), nil, "", "", "", repository.NewAdminAuditEventRepository())
		h := handlers.NewStorageConfigHandlers(svc)
		r := newRouter(h.Routes)

		req := withClaims(httptest.NewRequest("GET", "/gdrive/oauth/authorize-url?folderId=folder-1", nil), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("configured client returns a redirect url", func(t *testing.T) {
		oauthStates := service.NewOAuthStateService(pool, repository.NewOAuthStateRepository())
		svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir(),
			oauthStates, "test-client-id", "test-client-secret", "https://argusops.example/auth/oauth/gdrive/callback", repository.NewAdminAuditEventRepository())
		h := handlers.NewStorageConfigHandlers(svc)
		r := newRouter(h.Routes)

		req := withClaims(httptest.NewRequest("GET", "/gdrive/oauth/authorize-url?folderId=folder-1", nil), tenantID, userID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "accounts.google.com")
	})
}

func TestStorageConfigHandlers_MissingTenantContext(t *testing.T) {
	h := handlers.NewStorageConfigHandlers(nil)
	r := newRouter(h.Routes)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/"}, {"DELETE", "/"}, {"GET", "/gdrive/oauth/authorize-url"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
		})
	}
}
