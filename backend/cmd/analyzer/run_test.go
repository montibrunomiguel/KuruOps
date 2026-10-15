package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/events"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil/cmdtest"
)

func TestWakesQueue(t *testing.T) {
	assert.True(t, wakesQueue(events.Event{Type: "alert", Payload: map[string]any{"action": "received"}}))
	assert.False(t, wakesQueue(events.Event{Type: "alert", Payload: map[string]any{"action": "duplicate_suppressed"}}), "no new alert, no new job")
	assert.False(t, wakesQueue(events.Event{Type: "incident", Payload: map[string]any{"action": "received"}}))
	assert.False(t, wakesQueue(events.Event{Type: "alert", Payload: "received"}), "a payload that is not an object is ignored")
	assert.False(t, wakesQueue(events.Event{Type: "alert"}))
}

// TestAnalyzerRunsAJobIngestEnqueued is the architecture end to end: an alert is ingested
// (writing a job row in its own transaction, and publishing "alert received"), and the
// separately running analyzer -- which the test never calls directly -- picks the job up and
// analyses it. The poll interval is an hour, so the only way the job runs promptly is the
// wake-up event: that is the path that replaced the in-process wake when the consumer moved
// out of ingest.
func TestAnalyzerRunsAJobIngestEnqueued(t *testing.T) {
	var llmCalls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCalls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"## Alert Triage Report\nlooks benign"}}]}`))
	}))
	defer llm.Close()

	cfg := cmdtest.CommandConfig(t, "ALLOW_PRIVATE_NETWORK_TARGETS=true", "AI_ANALYSIS_POLL_INTERVAL=1h")
	base, stop := cmdtest.RunCommand(t, cfg, run)

	// Everything below plays the part of cmd/ingest and of an admin configuring a provider.
	ctx := t.Context()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL, db.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	admin := cmdtest.AdminConn(t, cfg)

	var tenantID, actorID uuid.UUID
	require.NoError(t, admin.QueryRow(ctx, `select id from tenants order by created_at limit 1`).Scan(&tenantID))
	require.NoError(t, admin.QueryRow(ctx, `select id from users where email = 'admin@kuruops.local'`).Scan(&actorID))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := secrets.NewFromConfig(ctx, cfg, pool)
	require.NoError(t, err)
	audit := repository.NewAdminAuditEventRepository()

	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store, audit)
	provider, err := llmSvc.Create(ctx, tenantID, actorID, service.LLMProviderSaveInput{
		Name: "p", Kind: "openai_compatible", BaseURL: &llm.URL, Model: "m", APIKey: "sk", AutoAnalyzeAllAlerts: true,
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(ctx, tenantID, actorID, provider.ID))

	endpoint, err := service.NewWebhookService(pool, repository.NewWebhookRepository(), audit).
		Create(ctx, tenantID, actorID, "ep", "test", nil, nil, nil, nil)
	require.NoError(t, err)

	broadcaster := events.NewBroadcaster(pool.Pool, logger)
	go broadcaster.Start(ctx)
	require.NoError(t, broadcaster.WaitReady(ctx))

	tagService := service.NewTagService(pool, repository.NewTagRepository(), audit)
	alerts := service.NewAlertService(pool, repository.NewAlertRepository(), tagService, repository.NewPlaybookRepository())
	alerts.EnableEventPublishing(broadcaster.Publish)
	alerts.EnableAutoAnalysisQueue(repository.NewAIAnalysisJobRepository(), nil)

	alert, _, err := alerts.Ingest(ctx, tenantID, endpoint.Endpoint.ID, domain.Alert{
		Title: "Suspicious login", Source: "test", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{"user":"alice"}`),
	}, nil, 0)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		var status string
		err := admin.QueryRow(ctx, `select status from ai_analysis_jobs where alert_id = $1`, alert.ID).Scan(&status)
		return err == nil && status == "succeeded"
	}, 20*time.Second, 100*time.Millisecond, "the analyzer never completed the job the ingest side enqueued (with a 1h poll, only the wake-up event can start it)")

	assert.EqualValues(t, 1, llmCalls.Load(), "exactly one analysis")
	var runStatus string
	require.NoError(t, admin.QueryRow(ctx, `select status from ai_analysis_runs where context_id = $1`, alert.ID).Scan(&runStatus))
	assert.Equal(t, "completed", runStatus)

	resp, err := http.Get(base + "/metrics")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Contains(t, string(body), `kuruops_ai_analysis_jobs{status="succeeded"} 1`, "the queue gauges are served by this process")
	assert.Contains(t, string(body), "kuruops_build_info")

	require.NoError(t, stop(), "run must return nil on a clean shutdown")
}

func TestAnalyzerRunFailsToStartCleanly(t *testing.T) {
	t.Run("database unreachable", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t, "DATABASE_URL=postgres://u:p@127.0.0.1:1/kuruops?sslmode=disable&connect_timeout=2")
		err := run(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.Error(t, err)
	})

	t.Run("an unknown secrets backend", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t, "SECRETS_BACKEND=carrier-pigeon")
		err := run(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "secrets backend")
	})

	t.Run("the HTTP port is already taken", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t)
		_, stopFirst := cmdtest.RunCommand(t, cfg, run)
		defer func() { _ = stopFirst() }()

		err := run(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.Error(t, err)
	})
}
