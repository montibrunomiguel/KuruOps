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
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir())
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
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir())
	h := handlers.NewStorageConfigHandlers(svc)
	r := newRouter(h.Routes)

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
