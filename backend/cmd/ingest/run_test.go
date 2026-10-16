package main

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil/cmdtest"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func post(t *testing.T, url, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("X-Webhook-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestRunReceivesWebhooksAndQueuesTheAnalysis starts the real ingest process and sends it
// webhooks: a valid token stores the alert AND, in the same transaction, the job that owes it
// an automatic analysis (which cmd/analyzer then works); anything without a valid token is
// refused. The process makes no LLM call itself -- that moved out with the consumer.
func TestRunReceivesWebhooksAndQueuesTheAnalysis(t *testing.T) {
	cfg := cmdtest.CommandConfig(t)
	base, stop := cmdtest.RunCommand(t, cfg, run)

	ctx := t.Context()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL, db.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	admin := cmdtest.AdminConn(t, cfg)

	var tenantID, actorID uuid.UUID
	require.NoError(t, admin.QueryRow(ctx, `select id from tenants order by created_at limit 1`).Scan(&tenantID))
	require.NoError(t, admin.QueryRow(ctx, `select id from users where email = 'admin@kuruops.local'`).Scan(&actorID))

	store, err := secrets.NewFromConfig(ctx, cfg, pool)
	require.NoError(t, err)
	audit := repository.NewAdminAuditEventRepository()

	// A default provider that auto-analyses: that is what makes Ingest enqueue a job. Its
	// URL is never dialled -- nothing in this process calls an LLM.
	llmURL := "http://127.0.0.1:1"
	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store, audit)
	provider, err := llmSvc.Create(ctx, tenantID, actorID, service.LLMProviderSaveInput{
		Name: "p", Kind: "openai_compatible", BaseURL: &llmURL, Model: "m", APIKey: "sk", AutoAnalyzeAllAlerts: true,
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(ctx, tenantID, actorID, provider.ID))

	endpoint, err := service.NewWebhookService(pool, repository.NewWebhookRepository(), audit).
		Create(ctx, tenantID, actorID, "ep", "test", nil, nil, nil, nil)
	require.NoError(t, err)

	t.Run("liveness, readiness and metrics", func(t *testing.T) {
		for _, path := range []string{"/livez", "/healthz"} {
			resp, err := http.Get(base + path)
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode, path)
		}
		resp, err := http.Get(base + "/metrics")
		require.NoError(t, err)
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Contains(t, string(b), "kuruops_build_info")
		assert.NotContains(t, string(b), "kuruops_ai_analysis_jobs", "queue gauges belong to the analyzer now")
	})

	t.Run("a webhook without a valid token is refused", func(t *testing.T) {
		code, _ := post(t, base+"/hooks", "", `{"title":"x","severity":"high"}`)
		assert.Equal(t, http.StatusUnauthorized, code)
		code, _ = post(t, base+"/hooks", "whk_not-a-real-token", `{"title":"x","severity":"high"}`)
		assert.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("a valid webhook stores the alert and its analysis job together", func(t *testing.T) {
		code, body := post(t, base+"/hooks", endpoint.Token, `{"title":"Suspicious login","severity":"high"}`)
		require.Equal(t, http.StatusCreated, code, body)

		var alertID uuid.UUID
		require.NoError(t, admin.QueryRow(ctx, `select id from alerts where title = 'Suspicious login'`).Scan(&alertID))
		var status string
		require.NoError(t, admin.QueryRow(ctx, `select status from ai_analysis_jobs where alert_id = $1`, alertID).Scan(&status))
		assert.Equal(t, "pending", status, "the job is owed, waiting for an analyzer")
	})

	require.NoError(t, stop(), "run must return nil on a clean shutdown")
}

func TestRunFailsToStartCleanly(t *testing.T) {
	t.Run("database unreachable", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t, "DATABASE_URL=postgres://u:p@127.0.0.1:1/kuruops?sslmode=disable&connect_timeout=2")
		err := run(t.Context(), cfg, quietLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database connection")
	})

	t.Run("the HTTP port is already taken", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t)
		_, stopFirst := cmdtest.RunCommand(t, cfg, run)
		defer func() { _ = stopFirst() }()

		err := run(t.Context(), cfg, quietLogger())
		require.Error(t, err)
	})
}
