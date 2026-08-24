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
	webhookSvc := service.NewWebhookService(pool, webhookRepo, repository.NewAdminAuditEventRepository())
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
	require.NoError(t, err)

	_, err = tagSvc.Create(t.Context(), tenantID, actorID, "phishing", nil)
	require.NoError(t, err)

	fieldMappingSvc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository())
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

// TestIngestHandler_AutoCreatesUnknownTags is the regression test for the
// TagService.EnsureExist switch: a tag the source sends that isn't yet in
// Settings -> Tags used to be silently dropped (see the now-superseded
// TagService.FilterKnown) -- it must now be auto-created in the tenant's
// catalog and actually attached to the ingested alert, exactly like a
// pre-registered tag already was.
func TestIngestHandler_AutoCreatesUnknownTags(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	webhookRepo := repository.NewWebhookRepository()
	webhookSvc := service.NewWebhookService(pool, webhookRepo, repository.NewAdminAuditEventRepository())
	tagRepo := repository.NewTagRepository()
	tagSvc := service.NewTagService(pool, tagRepo, repository.NewAdminAuditEventRepository())
	alertRepo := repository.NewAlertRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc, repository.NewPlaybookRepository())

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Custom SIEM", "some_custom_siem", nil, nil, nil, nil)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository()), logger)

	body, _ := json.Marshal(map[string]any{
		"title": "Suspicious login", "severity": "high", "tags": []string{"brand-new-tag", "  another-new-one  "},
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
		assert.ElementsMatch(t, []string{"brand-new-tag", "another-new-one"}, alert.Tags, "trimmed, and both attached even though neither existed before this request")
		return nil
	}))

	catalog, err := tagSvc.List(t.Context(), tenantID)
	require.NoError(t, err)
	names := make([]string, len(catalog))
	for i, tg := range catalog {
		names[i] = tg.Name
	}
	assert.Contains(t, names, "brand-new-tag", "auto-created tags must show up in Settings -> Tags")
	assert.Contains(t, names, "another-new-one")
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
	webhookSvc := service.NewWebhookService(pool, webhookRepo, repository.NewAdminAuditEventRepository())
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Custom SIEM", "some_custom_siem", nil, nil, nil, nil)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository()), logger)

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
	webhookSvc := service.NewWebhookService(pool, webhookRepo, repository.NewAdminAuditEventRepository())
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertRepo := repository.NewAlertRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc, repository.NewPlaybookRepository())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository()), logger)

	t.Run("a metadata object is stored verbatim", func(t *testing.T) {
		result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "SIEM A", "siem-a", nil, nil, nil, nil)
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
		result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "SIEM B", "siem-b", nil, nil, nil, nil)
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
		result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "SIEM C", "siem-c", nil, nil, nil, nil)
		require.NoError(t, err)

		body, _ := json.Marshal(map[string]any{"title": "Weird metadata", "severity": "low", "metadata": []string{"not", "an", "object"}})
		req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
		req.Header.Set("X-Webhook-Token", result.Token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusCreated, rec.Code, "malformed metadata must not fail the whole ingest")
	})

	t.Run("field mapping template adds extra fields, auto metadata wins on label conflict", func(t *testing.T) {
		fieldMappingSvc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository())
		template, err := fieldMappingSvc.Create(t.Context(), tenantID, actorID, "SIEM D fields", []domain.FieldMappingRule{
			{JSONPath: "rule.level", Label: "Rule Level"},
			{JSONPath: "environment", Label: "environment"}, // collides with the sender's own metadata.environment below
		})
		require.NoError(t, err)

		result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "SIEM D", "siem-d", nil, &template.ID, nil, nil)
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
	webhookSvc := service.NewWebhookService(pool, webhookRepo, repository.NewAdminAuditEventRepository())
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Disabled Endpoint", "wazuh", nil, nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, webhookSvc.SetStatus(t.Context(), tenantID, actorID, result.Endpoint.ID, "disabled"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository()), logger)

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
	webhookSvc := service.NewWebhookService(pool, webhookRepo, repository.NewAdminAuditEventRepository())
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Expiring Endpoint", "wazuh", nil, nil, nil, nil)
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
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository()), logger)

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

// TestIngestHandler_Dedup exercises the group-by-fields dedup feature
// end-to-end through the real HTTP handler: same group-key value within the
// window suppresses and increments duplicate_count on the original alert
// (200, not 201); a different value, or a payload missing the configured
// field entirely, always inserts a new alert (201) -- the field-missing case
// is the confirmed "never dedup on shared absence" decision.
func TestIngestHandler_Dedup(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	webhookRepo := repository.NewWebhookRepository()
	webhookSvc := service.NewWebhookService(pool, webhookRepo, repository.NewAdminAuditEventRepository())
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertRepo := repository.NewAlertRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc, repository.NewPlaybookRepository())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository()), logger)

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Dedup Endpoint", "some_custom_siem", nil, nil, []string{"host.name"}, nil)
	require.NoError(t, err)

	post := func(payload map[string]any) (*httptest.ResponseRecorder, string) {
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
		req.Header.Set("X-Webhook-Token", result.Token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var resp map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec, resp["id"]
	}

	countAlerts := func() int {
		var n int
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			list, err := alertRepo.List(t.Context(), tx, repository.ListAlertsFilter{})
			if err != nil {
				return err
			}
			n = len(list)
			return nil
		}))
		return n
	}

	rec1, id1 := post(map[string]any{"title": "Suspicious login", "severity": "high", "host": map[string]any{"name": "srv-1"}})
	require.Equal(t, http.StatusCreated, rec1.Code)
	assert.Equal(t, 1, countAlerts())

	t.Run("same host.name within the window suppresses and increments the original", func(t *testing.T) {
		rec2, id2 := post(map[string]any{"title": "Suspicious login", "severity": "high", "host": map[string]any{"name": "srv-1"}})
		require.Equal(t, http.StatusOK, rec2.Code)
		assert.Equal(t, id1, id2, "the deduped response still returns the original alert's id")
		assert.Equal(t, 1, countAlerts(), "no new alert row was inserted")

		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			alertID, err := uuid.Parse(id1)
			require.NoError(t, err)
			alert, err := alertRepo.Get(t.Context(), tx, alertID)
			require.NoError(t, err)
			require.NotNil(t, alert)
			assert.Equal(t, 1, alert.DuplicateCount)
			return nil
		}))
	})

	t.Run("a different host.name is a new alert", func(t *testing.T) {
		rec3, id3 := post(map[string]any{"title": "Suspicious login", "severity": "high", "host": map[string]any{"name": "srv-2"}})
		require.Equal(t, http.StatusCreated, rec3.Code)
		assert.NotEqual(t, id1, id3)
		assert.Equal(t, 2, countAlerts())
	})

	t.Run("a payload missing the configured field never dedups", func(t *testing.T) {
		rec4, id4 := post(map[string]any{"title": "Suspicious login", "severity": "high"})
		require.Equal(t, http.StatusCreated, rec4.Code)
		assert.NotEqual(t, id1, id4)
		assert.Equal(t, 3, countAlerts())

		rec5, id5 := post(map[string]any{"title": "Suspicious login", "severity": "high"})
		require.Equal(t, http.StatusCreated, rec5.Code, "still no dedup the second time -- absence never matches absence")
		assert.NotEqual(t, id4, id5)
		assert.Equal(t, 4, countAlerts())
	})
}
