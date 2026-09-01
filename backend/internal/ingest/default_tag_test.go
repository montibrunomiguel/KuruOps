package ingest_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/ingest"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// TestIngestHandler_DefaultsTagToEndpointName covers the fix for the sweep's
// blind spot: tag visibility is an intersection, so an alert with no tags
// overlapped with nothing and was invisible to every tag-restricted role.
// Freshly ingested alerts were visible to nobody who was supposed to triage
// them, with no error and no empty-queue indicator anywhere.
//
// Naming the fallback tag after the endpoint keeps the fail-closed rule
// intact (nothing became visible to a role that was not granted it) while
// making the tag meaningful and grantable.
func TestIngestHandler_DefaultsTagToEndpointName(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	webhookRepo := repository.NewWebhookRepository()
	webhookSvc := service.NewWebhookService(pool, webhookRepo, repository.NewAdminAuditEventRepository())
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertRepo := repository.NewAlertRepository()
	alertSvc := service.NewAlertService(pool, alertRepo, tagSvc, repository.NewPlaybookRepository())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc,
		service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository()), logger)

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "Corp EDR", "some_custom_siem", nil, nil, nil, nil)
	require.NoError(t, err)

	post := func(t *testing.T, payload map[string]any) uuid.UUID {
		t.Helper()
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
		req.Header.Set("X-Webhook-Token", result.Token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var resp map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		id, err := uuid.Parse(resp["id"])
		require.NoError(t, err)
		return id
	}

	tagsOf := func(t *testing.T, alertID uuid.UUID) []string {
		t.Helper()
		var tags []string
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			alert, err := alertRepo.Get(t.Context(), tx, alertID)
			require.NoError(t, err)
			require.NotNil(t, alert)
			tags = alert.Tags
			return nil
		}))
		return tags
	}

	t.Run("an alert with no tags gets the endpoint's name", func(t *testing.T) {
		id := post(t, map[string]any{"title": "no tags here", "severity": "high"})
		assert.Equal(t, []string{"Corp EDR"}, tagsOf(t, id),
			"an untagged alert would be invisible to every tag-restricted analyst")
	})

	t.Run("the fallback tag is registered in the catalog, so a role can be granted it", func(t *testing.T) {
		post(t, map[string]any{"title": "another", "severity": "low"})
		catalog, err := tagSvc.List(t.Context(), tenantID)
		require.NoError(t, err)
		names := make([]string, len(catalog))
		for i, tg := range catalog {
			names[i] = tg.Name
		}
		assert.Contains(t, names, "Corp EDR",
			"a tag nobody can grant would not fix the blind spot, only rename it")
	})

	t.Run("tags the source does send are left alone", func(t *testing.T) {
		id := post(t, map[string]any{"title": "tagged", "severity": "high", "tags": []string{"finance"}})
		assert.Equal(t, []string{"finance"}, tagsOf(t, id),
			"the fallback applies only when the source supplied nothing")
	})

	t.Run("an explicitly empty tag list still gets the fallback", func(t *testing.T) {
		id := post(t, map[string]any{"title": "empty list", "severity": "high", "tags": []string{}})
		assert.Equal(t, []string{"Corp EDR"}, tagsOf(t, id))
	})
}
