package handlers_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/db"
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
	h, alertSvc, tenantID, _ = setupUploadHandlersWithPool(t)
	return h, alertSvc, tenantID
}

// setupUploadHandlersWithPool is setupUploadHandlers plus the raw pool, for
// the handful of tests that need to reach into upload_keys directly (e.g.
// simulating a pre-migration key with no row) rather than through
// UploadHandlers' own routes.
func setupUploadHandlersWithPool(t *testing.T) (h *handlers.UploadHandlers, alertSvc *service.AlertService, tenantID uuid.UUID, pool *db.Pool) {
	t.Helper()
	pool = testutil.RequireTestDB(t)
	dir := t.TempDir()

	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc = service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incidentSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	storageSvc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir)
	uploadKeySvc := service.NewUploadKeyService(pool, repository.NewUploadKeyRepository())

	h = handlers.NewUploadHandlers(storageSvc, alertSvc, incidentSvc, uploadKeySvc)
	tenantID = testutil.NewTenant(t)
	return h, alertSvc, tenantID, pool
}

// TestUploadHandlers_UploadFailure_LocalStoreError is the regression test
// for the generic "could not store uploaded file" message: it used to fire
// for every store.Put failure alike, even though "configure storage in
// Settings" (the user's original assumption) is never actually the cause --
// BuildStore always falls back to a working LocalStore when nothing is
// configured. This forces a real LocalStore.Put failure (UPLOAD_DIR points
// at a plain file, so os.MkdirAll can't create anything under it) and
// confirms the response steers toward "contact your administrator" instead
// of a Settings page that wouldn't help.
func TestUploadHandlers_UploadFailure_LocalStoreError(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	blockedDir := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blockedDir, []byte("i'm a file, not a directory"), 0o644))

	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incidentSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	storageSvc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), blockedDir)
	uploadKeySvc := service.NewUploadKeyService(pool, repository.NewUploadKeyRepository())
	h := handlers.NewUploadHandlers(storageSvc, alertSvc, incidentSvc, uploadKeySvc)
	tenantID := testutil.NewTenant(t)
	r := newRouter(h.Routes)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "a", Source: "s", Severity: domain.SeverityLow, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	req := multipartUploadRequest(t, map[string]string{"kind": "alert", "id": alert.ID.String()}, tinyPNG(t))
	req = withClaims(req, tenantID, uuid.New(), nil)
	rec := doRequest(r, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "contact your administrator")
	assert.NotContains(t, rec.Body.String(), "Settings", "no storage integration is configured here, so the message must not send the caller looking for one")
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

// TestUploadHandlers_ServeEnforcesAllowedTags is the regression test for
// Frente 5: GET used to only check tenantID and the key's shape, never
// re-applying the allowedTags visibility rule POST already enforces via
// resolveEntity -- an analyst without access to a tag-restricted alert
// could still fetch its attachments directly by URL. The alert here is
// untagged, so a caller with any non-empty allowedTags scope has nothing in
// common with it (see service.tagsVisible) and must be refused.
func TestUploadHandlers_ServeEnforcesAllowedTags(t *testing.T) {
	h, alertSvc, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Tag-Restricted Evidence", Source: "test", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
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
	key := resp.URL[len("/api/v1/uploads/images/"):]

	t.Run("caller without access to the alert's tags -- 404", func(t *testing.T) {
		getReq := withClaims(httptest.NewRequest("GET", "/"+key, nil), tenantID, uuid.New(), []string{"unrelated-tag"})
		getRec := doRequest(r, getReq)
		assert.Equal(t, http.StatusNotFound, getRec.Code)
	})

	t.Run("caller with unrestricted access -- still served", func(t *testing.T) {
		getReq := withClaims(httptest.NewRequest("GET", "/"+key, nil), tenantID, uuid.New(), nil)
		getRec := doRequest(r, getReq)
		assert.Equal(t, http.StatusOK, getRec.Code)
	})
}

// TestUploadHandlers_ServePreMigrationKeyIgnoresTags confirms a key with no
// upload_keys row (every key uploaded before that table existed) still
// serves normally regardless of the caller's tag scope, instead of
// retroactively breaking every attachment uploaded before this feature
// shipped.
func TestUploadHandlers_ServePreMigrationKeyIgnoresTags(t *testing.T) {
	h, alertSvc, tenantID, pool := setupUploadHandlersWithPool(t)
	r := newRouter(h.Routes)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Pre-Migration Evidence", Source: "test", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
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
	key := resp.URL[len("/api/v1/uploads/images/"):]

	require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), "delete from upload_keys where key = $1", key)
		return err
	}))

	getReq := withClaims(httptest.NewRequest("GET", "/"+key, nil), tenantID, uuid.New(), []string{"unrelated-tag"})
	getRec := doRequest(r, getReq)
	assert.Equal(t, http.StatusOK, getRec.Code, "a key with no upload_keys row has nothing to check against, so it must still be served")
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

	for _, filename := range []string{"totally-safe.exe", "installer.msi", "script.ps1", "run-me.sh", "payload.jar"} {
		req := multipartUploadRequestNamed(t, map[string]string{"kind": "alert", "id": alert.ID.String()}, filename, []byte("MZ fake binary"))
		req = withClaims(req, tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "filename %q must be rejected", filename)
	}
}

// TestUploadHandlers_AllowsArbitraryNonExecutableExtension confirms the
// deny-list (see deniedAttachmentExtensions) is permissive by default --
// only executables/installers/scripts are blocked, everything else (even
// an extension no fixed allow-list would have anticipated) is accepted, as
// long as it's zip-shaped enough to satisfy attachmentExtRE.
func TestUploadHandlers_AllowsArbitraryNonExecutableExtension(t *testing.T) {
	h, alertSvc, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "a", Source: "s", Severity: domain.SeverityLow, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	for _, filename := range []string{"evidence.rar", "notes.md", "capture.7z", "export.parquet", "dump.sqlite"} {
		req := multipartUploadRequestNamed(t, map[string]string{"kind": "alert", "id": alert.ID.String()}, filename, []byte("arbitrary evidence bytes"))
		req = withClaims(req, tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusCreated, rec.Code, "filename %q must be accepted, got %s", filename, rec.Body.String())
	}
}

// TestUploadHandlers_RejectsFakeImageExtension confirms a file whose bytes
// don't actually sniff as an image can't slip in disguised with an image
// extension -- see imageOnlyExtensions' doc comment for why this matters
// beyond just "the label is wrong" (LocalStore recomputes Content-Type from
// the extension alone on every GET).
func TestUploadHandlers_RejectsFakeImageExtension(t *testing.T) {
	h, alertSvc, tenantID := setupUploadHandlers(t)
	r := newRouter(h.Routes)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "a", Source: "s", Severity: domain.SeverityLow, Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	req := multipartUploadRequestNamed(t, map[string]string{"kind": "alert", "id": alert.ID.String()}, "not-really-a.png", []byte("plain text, not a real png"))
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
	h := handlers.NewUploadHandlers(nil, nil, nil, nil)
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
