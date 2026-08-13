package ingest_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/ingest"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newIngestHandlerFixture(t *testing.T) (h *ingest.Handler, token string) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	webhookRepo := repository.NewWebhookRepository()
	webhookSvc := service.NewWebhookService(pool, webhookRepo)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil)
	require.NoError(t, err)

	_, err = tagSvc.Create(t.Context(), tenantID, actorID, "phishing", nil)
	require.NoError(t, err)

	fieldMappingSvc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h = ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, fieldMappingSvc, logger)
	return h, result.Token
}

func TestIngestHandler_MethodNotAllowed(t *testing.T) {
	h, _ := newIngestHandlerFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/hooks", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestIngestHandler_MissingToken(t *testing.T) {
	h, _ := newIngestHandlerFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestIngestHandler_InvalidToken(t *testing.T) {
	h, _ := newIngestHandlerFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Webhook-Token", "whk_not-a-real-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestIngestHandler_ValidAlert posts a Wazuh-shaped body -- the fixture's
// endpoint is registered with source="wazuh" (see newIngestHandlerFixture),
// so ServeHTTP routes it through wazuhNormalizer, not the flat generic
// envelope. See TestIngestHandler_UnknownSourceFallsBackToGeneric for the
// generic-envelope path.
func TestIngestHandler_ValidAlert(t *testing.T) {
	h, token := newIngestHandlerFixture(t)

	body, _ := json.Marshal(map[string]any{
		"rule": map[string]any{"level": 10, "description": "Suspicious login", "groups": []string{"phishing", "not-registered-anywhere"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["id"])
}

// TestIngestHandler_UnknownSourceFallsBackToGeneric guards the fallback
// path: a webhook endpoint whose source has no dedicated normalizer still
// ingests via the flat genericNormalizer envelope, same as before per-source
// routing existed.
func TestIngestHandler_UnknownSourceFallsBackToGeneric(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	webhookRepo := repository.NewWebhookRepository()
	webhookSvc := service.NewWebhookService(pool, webhookRepo)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Custom SIEM", "some_custom_siem", nil, nil)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository()), logger)

	body, _ := json.Marshal(map[string]any{"title": "Suspicious login", "severity": "high"})
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Token", result.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
}

// TestIngestHandler_Metadata guards extractMetadata's passthrough of an
// optional, generic top-level "metadata" object -- independent of which
// vendor normalizer runs (uses the generic envelope here, but extraction
// happens in ServeHTTP itself, not per-normalizer).
func TestIngestHandler_Metadata(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	webhookRepo := repository.NewWebhookRepository()
	webhookSvc := service.NewWebhookService(pool, webhookRepo)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertRepo := repository.NewAlertRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository()), logger)

	t.Run("a metadata object is stored verbatim", func(t *testing.T) {
		result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "SIEM A", "siem-a", nil, nil)
		require.NoError(t, err)

		body, _ := json.Marshal(map[string]any{
			"title": "Suspicious login", "severity": "high",
			"metadata": map[string]any{"slackChannel": "#incident-response", "environment": "production"},
		})
		req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
		req.Header.Set("X-Webhook-Token", result.Token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)

		var resp map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		alertID, err := uuid.Parse(resp["id"])
		require.NoError(t, err)

		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			alert, err := alertRepo.Get(t.Context(), tx, alertID)
			require.NoError(t, err)
			require.NotNil(t, alert)
			var meta map[string]string
			require.NoError(t, json.Unmarshal(alert.Metadata, &meta))
			assert.Equal(t, "#incident-response", meta["slackChannel"])
			assert.Equal(t, "production", meta["environment"])
			return nil
		}))
	})

	t.Run("no metadata field -- stored as an empty object, not null", func(t *testing.T) {
		result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "SIEM B", "siem-b", nil, nil)
		require.NoError(t, err)

		body, _ := json.Marshal(map[string]any{"title": "No metadata here", "severity": "low"})
		req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
		req.Header.Set("X-Webhook-Token", result.Token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)

		var resp map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		alertID, err := uuid.Parse(resp["id"])
		require.NoError(t, err)

		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			alert, err := alertRepo.Get(t.Context(), tx, alertID)
			require.NoError(t, err)
			require.NotNil(t, alert)
			assert.JSONEq(t, `{}`, string(alert.Metadata))
			return nil
		}))
	})

	t.Run("metadata sent as a non-object is dropped, not a hard failure", func(t *testing.T) {
		result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "SIEM C", "siem-c", nil, nil)
		require.NoError(t, err)

		body, _ := json.Marshal(map[string]any{"title": "Weird metadata", "severity": "low", "metadata": []string{"not", "an", "object"}})
		req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
		req.Header.Set("X-Webhook-Token", result.Token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusCreated, rec.Code, "malformed metadata must not fail the whole ingest")
	})

	t.Run("field mapping template adds extra fields, auto metadata wins on label conflict", func(t *testing.T) {
		fieldMappingSvc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository())
		template, err := fieldMappingSvc.Create(t.Context(), tenantID, actorID, "SIEM D fields", []domain.FieldMappingRule{
			{JSONPath: "rule.level", Label: "Rule Level"},
			{JSONPath: "environment", Label: "environment"}, // collides with the sender's own metadata.environment below
		})
		require.NoError(t, err)

		result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "SIEM D", "siem-d", nil, &template.ID)
		require.NoError(t, err)

		body, _ := json.Marshal(map[string]any{
			"title": "Templated fields", "severity": "medium",
			"metadata":    map[string]any{"environment": "production"},
			"rule":        map[string]any{"level": 7},
			"environment": "staging", // shadowed by the metadata.environment above once merged
		})
		req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
		req.Header.Set("X-Webhook-Token", result.Token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)

		var resp map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		alertID, err := uuid.Parse(resp["id"])
		require.NoError(t, err)

		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			alert, err := alertRepo.Get(t.Context(), tx, alertID)
			require.NoError(t, err)
			require.NotNil(t, alert)
			var meta map[string]any
			require.NoError(t, json.Unmarshal(alert.Metadata, &meta))
			assert.Equal(t, float64(7), meta["Rule Level"], "rule.level should be pulled in under its configured label")
			assert.Equal(t, "production", meta["environment"], "the sender's own metadata.environment must win over the template's conflicting rule")
			return nil
		}))
	})
}

func TestIngestHandler_MalformedBody(t *testing.T) {
	h, token := newIngestHandlerFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader([]byte("not json")))
	req.Header.Set("X-Webhook-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestIngestHandler_MissingRequiredField(t *testing.T) {
	h, token := newIngestHandlerFixture(t)
	body, _ := json.Marshal(map[string]any{"severity": "high"})
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestIngestHandler_DisabledEndpoint(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	webhookRepo := repository.NewWebhookRepository()
	webhookSvc := service.NewWebhookService(pool, webhookRepo)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Disabled Endpoint", "wazuh", nil, nil)
	require.NoError(t, err)
	require.NoError(t, webhookSvc.SetStatus(t.Context(), tenantID, result.Endpoint.ID, "disabled"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository()), logger)

	body, _ := json.Marshal(map[string]any{"title": "t", "severity": "low"})
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Token", result.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestIngestHandler_ExpiredToken(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	webhookRepo := repository.NewWebhookRepository()
	webhookSvc := service.NewWebhookService(pool, webhookRepo)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Expiring Endpoint", "wazuh", nil, nil)
	require.NoError(t, err)

	// RotateToken keeps the same plaintext token's hash but overwrites
	// expires_at directly -- the only way to get a token that is both
	// resolvable and already expired, since WebhookService.Create/Regenerate
	// always compute a future expiry.
	tokenHash := sha256Hex(result.Token)
	pastExpiry := time.Now().Add(-time.Hour)
	require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		return webhookRepo.RotateToken(t.Context(), tx, result.Endpoint.ID, tokenHash, "expd", &pastExpiry)
	}))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository()), logger)

	body, _ := json.Marshal(map[string]any{"title": "t", "severity": "low"})
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Token", result.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
