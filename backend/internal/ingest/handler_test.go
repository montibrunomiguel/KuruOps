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

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil)
	require.NoError(t, err)

	_, err = tagSvc.Create(t.Context(), tenantID, actorID, "phishing", nil)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h = ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, logger)
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

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Custom SIEM", "some_custom_siem", nil)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, logger)

	body, _ := json.Marshal(map[string]any{"title": "Suspicious login", "severity": "high"})
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Token", result.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
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

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Disabled Endpoint", "wazuh", nil)
	require.NoError(t, err)
	require.NoError(t, webhookSvc.SetStatus(t.Context(), tenantID, result.Endpoint.ID, "disabled"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, logger)

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

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Expiring Endpoint", "wazuh", nil)
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
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, logger)

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
