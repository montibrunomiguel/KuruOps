package handlers_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func multipartUploadRequest(t *testing.T, fields map[string]string, fileContent []byte) *http.Request {
	t.Helper()
	return multipartUploadRequestNamed(t, fields, "screenshot.png", fileContent)
}

func multipartUploadRequestNamed(t *testing.T, fields map[string]string, filename string, fileContent []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	part, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(fileContent)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequest("POST", "/", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// setupUploadHandlers wires a real UploadHandlers against the test DB (its
// alert/incident lookups go through AlertService.Get/IncidentService.Get,
// which need a real tx) with an unconfigured StorageConfigService -- no
// tenant_storage_config row means BuildStore falls back to a temp-dir
// LocalStore, so these tests never touch S3/GCS.
func setupUploadHandlers(t *testing.T) (h *handlers.UploadHandlers, alertSvc *service.AlertService, tenantID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	dir := t.TempDir()

	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc = service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incidentSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	storageSvc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir)

	h = handlers.NewUploadHandlers(storageSvc, alertSvc, incidentSvc)
	tenantID = testutil.NewTenant(t)
	return h, alertSvc, tenantID
}

func TestUploadHandlers_UploadAndServe(t *testing.T) {
	h, alertSvc, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Outbound C2 Traffic", Source: "test", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	req := multipartUploadRequest(t, map[string]string{"kind": "alert", "id": alert.ID.String()}, tinyPNG(t))
	req = withClaims(req, tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var resp struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Regexp(t, `^/api/v1/uploads/images/Alert/\d{4}/\d{2}/\d{2}/Outbound-C2-Traffic/[0-9a-f-]{36}_screenshot\.png$`, resp.URL)

	key := resp.URL[len("/api/v1/uploads/images/"):]
	getReq := httptest.NewRequest("GET", "/"+key, nil)
	getReq = withClaims(getReq, tenantID, uuid.New(), nil)
	getRec := doRequest(r, getReq)
	assert.Equal(t, http.StatusOK, getRec.Code)
	assert.Equal(t, tinyPNG(t), getRec.Body.Bytes())
	assert.Empty(t, getRec.Header().Get("Content-Disposition"), "images must stay inline")
}

func TestUploadHandlers_UploadAndServeNonImageAttachment(t *testing.T) {
	h, alertSvc, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Outbound C2 Traffic", Source: "test", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	req := multipartUploadRequestNamed(t, map[string]string{"kind": "alert", "id": alert.ID.String()}, "packet-capture.pcap", []byte("not really a pcap but the extension is what's checked"))
	req = withClaims(req, tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var resp struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Regexp(t, `^/api/v1/uploads/images/Alert/\d{4}/\d{2}/\d{2}/Outbound-C2-Traffic/[0-9a-f-]{36}_packet-capture\.pcap$`, resp.URL)

	key := resp.URL[len("/api/v1/uploads/images/"):]
	getReq := httptest.NewRequest("GET", "/"+key, nil)
	getReq = withClaims(getReq, tenantID, uuid.New(), nil)
	getRec := doRequest(r, getReq)
	assert.Equal(t, http.StatusOK, getRec.Code)
	assert.Equal(t, `attachment; filename="packet-capture.pcap"`, getRec.Header().Get("Content-Disposition"))
	assert.Equal(t, "nosniff", getRec.Header().Get("X-Content-Type-Options"))
}

func TestUploadHandlers_RejectsExecutableExtension(t *testing.T) {
	h, alertSvc, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "a", Source: "s", Severity: domain.SeverityLow, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	req := multipartUploadRequestNamed(t, map[string]string{"kind": "alert", "id": alert.ID.String()}, "totally-safe.exe", []byte("MZ fake binary"))
	req = withClaims(req, tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUploadHandlers_RejectsUnknownKind(t *testing.T) {
	h, _, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	req := multipartUploadRequest(t, map[string]string{"kind": "bogus", "id": tenantID.String()}, tinyPNG(t))
	req = withClaims(req, tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUploadHandlers_RejectsUnsupportedType(t *testing.T) {
	h, alertSvc, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "a", Source: "s", Severity: domain.SeverityLow, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	req := multipartUploadRequest(t, map[string]string{"kind": "alert", "id": alert.ID.String()}, []byte("not an image"))
	req = withClaims(req, tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUploadHandlers_RejectsMissingId(t *testing.T) {
	h, _, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	req := multipartUploadRequest(t, map[string]string{"kind": "alert"}, tinyPNG(t))
	req = withClaims(req, tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUploadHandlers_ServeRejectsInvalidKey(t *testing.T) {
	h, _, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	for _, key := range []string{"Alert/not-a-date/x", "Weird/2026/08/04/abc/file.png", "Alert/2026/08/04/x.exe"} {
		req := httptest.NewRequest("GET", "/"+key, nil)
		req = withClaims(req, tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "key %q must be rejected", key)
	}
}

func TestUploadHandlers_MissingTenantContext(t *testing.T) {
	h := handlers.NewUploadHandlers(nil, nil, nil)
	r := newRouter(h.Routes)

	for _, tc := range []struct{ method, path string }{
		{"POST", "/"}, {"GET", "/Alert/2026/01/01/some-key.png"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
		})
	}
}
