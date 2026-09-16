package ingest_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/ingest"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// newGenericIngestFixture mirrors newIngestHandlerFixture but registers a
// "generic" endpoint, so the tests below can post a plain {title, severity}
// body instead of a vendor envelope.
func newGenericIngestFixture(t *testing.T) (*ingest.Handler, string) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	webhookRepo := repository.NewWebhookRepository()
	webhookSvc := service.NewWebhookService(pool, webhookRepo, repository.NewAdminAuditEventRepository())
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())

	result, err := webhookSvc.Create(t.Context(), tenantID, actorID, "QA Generic", "generic", nil, nil, nil, nil)
	require.NoError(t, err)

	fieldMappingSvc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return ingest.NewHandler(pool, webhookRepo, alertSvc, tagSvc, fieldMappingSvc, logger), result.Token
}

func postHook(t *testing.T, h *ingest.Handler, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hooks", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestOversizedBodyIsRejectedAsTooLarge covers a diagnosis, not a limit.
//
// The 1 MiB cap was always there, but it was applied with io.LimitReader,
// which reports EOF at the limit rather than an error. ReadAll therefore
// succeeded with a *truncated* body, and the JSON decoder downstream
// answered:
//
//	decode webhook body: unexpected end of JSON input
//
// So the one thing the sender needed to know -- that the payload was too
// big -- was the one thing the error never said, and an integrator would go
// hunting through their own serializer instead. This asserts the size is now
// named, and with a status that means it.
func TestOversizedBodyIsRejectedAsTooLarge(t *testing.T) {
	h, token := newGenericIngestFixture(t)

	oversized := map[string]any{
		"title":    "QA oversized",
		"severity": "high",
		"blob":     strings.Repeat("x", 2<<20), // 2 MiB, comfortably past the cap
	}
	body, err := json.Marshal(oversized)
	require.NoError(t, err)
	require.Greater(t, len(body), 1<<20)

	rec := postHook(t, h, token, body)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code,
		"a payload over the cap is a 413, not a 400 about malformed JSON")

	got := strings.ToLower(rec.Body.String())
	assert.Contains(t, got, "limit", "the message has to say the body was too large")
	assert.NotContains(t, got, "json",
		"blaming JSON is what sent integrators debugging the wrong thing")
}

// TestBodyAtTheLimitStillWorks guards the other side of the change: raising
// an error at the boundary must not start rejecting payloads that were fine
// before. A large-but-legal alert is ordinary in security telemetry -- a
// full EDR process tree runs to hundreds of kilobytes.
func TestBodyAtTheLimitStillWorks(t *testing.T) {
	h, token := newGenericIngestFixture(t)

	for _, size := range []int{1 << 10, 256 << 10, 900 << 10} {
		t.Run(fmt.Sprintf("%dKiB", size>>10), func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"title":    "QA sized",
				"severity": "high",
				"blob":     strings.Repeat("x", size),
			})
			require.NoError(t, err)
			require.Less(t, len(body), 1<<20)

			rec := postHook(t, h, token, body)
			assert.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
		})
	}
}
