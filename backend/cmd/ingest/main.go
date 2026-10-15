// Command ingest is the only service that accepts inbound webhook traffic
// from SIEM/XDR sources. It is kept separate from cmd/api so it can be
// scaled, rate-limited, and network-policied independently of analyst-facing
// traffic — a burst of alerts from a source should never be able to starve
// the dashboard.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kuruops/kuruops/internal/buildinfo"
	"github.com/kuruops/kuruops/internal/circuitbreaker"
	"github.com/kuruops/kuruops/internal/config"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/events"
	"github.com/kuruops/kuruops/internal/httpserver"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/ingest"
	"github.com/kuruops/kuruops/internal/llmclient"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/safego"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/telemetry"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load(logger)
	if err != nil {
		logger.Error("config load failed", "error", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		logger.Error("refusing to start: insecure configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	otelShutdown, tracer, err := telemetry.Setup(ctx, "kuruops-ingest", cfg.OTelExporterOTLPEndpoint)
	if err != nil {
		logger.Error("telemetry setup failed", "error", err)
		os.Exit(1)
	}

	// This process makes LLM and MCP calls too (the automatic analysis of every
	// ingested alert), but never installed the circuit breaker the api and
	// worker do -- so during a provider outage every ingested alert dialled the
	// dead endpoint and waited out the full timeout, even though docker-compose
	// and the k8s ConfigMap pass EXTERNAL_CALL_BREAKER_* to it. Same settings,
	// same place as cmd/api, plus the in-place retry.
	breakerCfg := circuitbreaker.Config{
		Threshold: cfg.ExternalCallBreakerThreshold,
		Cooldown:  cfg.ExternalCallBreakerCooldown,
	}
	// Owned by this process and handed to the analysis service: the retry policy
	// and the per-host breakers every provider call shares.
	llmFactory := llmclient.NewFactory(llmclient.Options{
		Retry:   llmclient.RetryPolicy{MaxAttempts: cfg.LLMCallMaxAttempts, BaseDelay: cfg.LLMCallRetryBaseDelay},
		Breaker: breakerCfg,
	})
	mcpBreakers := circuitbreaker.NewRegistry(breakerCfg)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			logger.Warn("telemetry shutdown failed", "error", err)
		}
	}()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, db.PoolConfig{MaxConns: cfg.DBPoolMaxConns, MinConns: cfg.DBPoolMinConns, Tracer: tracer})
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	httpserver.GetMetrics().SetPool(pool.Pool)

	// ingest only ever calls TagService.EnsureExist/FilterKnown (webhook tag
	// catalog matching), never Create/Delete -- this dependency is never
	// actually exercised here, just required to satisfy the constructor.
	tagRepo := repository.NewTagRepository()
	tagService := service.NewTagService(pool, tagRepo, repository.NewAdminAuditEventRepository())
	alertRepo := repository.NewAlertRepository()
	alertService := service.NewAlertService(pool, alertRepo, tagService, repository.NewPlaybookRepository())

	// eventBroadcaster publishes to the same Postgres NOTIFY channel
	// cmd/api's own Broadcaster listens on -- an alert ingested here reaches
	// any connected browser tab (which is only ever talking to cmd/api),
	// even though this is a different process. See internal/events'
	// package doc comment.
	eventBroadcaster := events.NewBroadcaster(pool.Pool, logger)
	safego.Go("ingest.eventBroadcaster", func() { eventBroadcaster.Start(ctx) })
	alertService.EnableEventPublishing(eventBroadcaster.Publish)

	// On-call auto-assign is enabled only here, not in cmd/api -- it's a
	// property of the ingest path (see AlertService.Ingest), not something
	// the analyst-facing API needs to know about.
	onCallShiftService := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository(), repository.NewAdminAuditEventRepository())
	alertService.EnableOnCallAutoAssign(onCallShiftService)

	// Auto-analysis is enabled only here too, for the same reason -- see
	// AlertService.EnableAutoAnalysisQueue. This duplicates a chunk of cmd/api's
	// own AIAnalysisService wiring (secrets store, MCP tool service, LLM
	// provider repo); each cmd/* binary constructs whatever services it
	// needs independently (see cmd/worker's own secrets.Store setup) rather
	// than sharing a wiring package across processes.
	secretStore, err := secrets.NewFromConfig(ctx, cfg, pool)
	if err != nil {
		logger.Error("secrets backend setup failed", "backend", cfg.SecretsBackend, "error", err)
		os.Exit(1)
	}
	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolService := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	mcpToolService.SetCircuitBreakers(mcpBreakers)
	aiAnalysisService := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), alertRepo, repository.NewIncidentRepository(), secretStore,
		mcpServerRepo, mcpToolService, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
		service.WithLLMClientFactory(llmFactory),
	)
	mcpToolService.SetOnToolCallResolved(aiAnalysisService.ResumeAnalysisRun)

	// Without this the end of an automatic analysis never reached the browser:
	// the service's "analyzed" and per-turn events went nowhere in this process,
	// so an open alert sat on "Analyzing..." until the page was reloaded. cmd/api
	// wires the same call for the analyses it runs.
	aiAnalysisService.EnableEventPublishing(eventBroadcaster.Publish)

	// The automatic analysis of each ingested alert is a durable queue, not a
	// goroutine: Ingest writes a job row in the alert's own transaction (see
	// AlertService.EnableAutoAnalysisQueue) and this process works the rows with
	// a lease, retry and backoff -- so a restart, a deploy or a provider outage
	// no longer loses the analysis. It runs here, in the process that already
	// holds the LLM/MCP/secrets wiring and an RLS-subject database role; running
	// it in the worker would mean a second pool as kuruops_app (see the note in
	// cmd/worker). Several ingest replicas share one queue safely.
	jobRepo := repository.NewAIAnalysisJobRepository()
	analysisQueue := service.NewAIAnalysisQueue(pool, jobRepo, aiAnalysisService, service.AIAnalysisQueueConfig{
		Concurrency: cfg.AIAnalysisConcurrency,
		// Leave room inside ShutdownTimeout for the HTTP server to stop too.
		DrainTimeout: max(cfg.ShutdownTimeout-3*time.Second, 5*time.Second),
	}, logger)
	alertService.EnableAutoAnalysisQueue(jobRepo, analysisQueue.Wake)
	httpserver.GetMetrics().SetAIQueueStats(analysisQueue.Stats)
	queueStopped := make(chan struct{})
	safego.Go("ingest.aiAnalysisQueue", func() {
		defer close(queueStopped)
		analysisQueue.Run(ctx)
	})

	webhookRepo := repository.NewWebhookRepository()
	fieldMappingTemplateService := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository())
	handler := ingest.NewHandler(pool, webhookRepo, alertService, tagService, fieldMappingTemplateService, logger)

	hookLimiter := middleware.NewRateLimiter(ctx, pool.Pool, "webhook_ip", cfg.WebhookRateLimitPerMinute, time.Minute)

	mux := http.NewServeMux()
	mux.Handle("/healthz", httpserver.HealthCheck(pool.Pool))
	mux.HandleFunc("/livez", httpserver.Livez)
	mux.HandleFunc("/metrics", httpserver.MetricsHandler)
	mux.Handle("/hooks", hookLimiter(handler))

	// The only internet-facing process, so it gets the tightest deadlines --
	// see httpserver.IngestTimeouts.
	srv := httpserver.NewServer(cfg.HTTPAddr, httpserver.WrapWithObservability(mux, logger, tracer), httpserver.IngestTimeouts)

	safego.Go("ingest.ListenAndServe", func() {
		logger.Info("ingest listening", "addr", cfg.HTTPAddr, "build", buildinfo.Build)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	})

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}

	// The queue stopped claiming the moment ctx was cancelled; wait for the
	// analyses it already started to finish (or be handed back, past its drain
	// timeout) so a deploy does not drop them.
	select {
	case <-queueStopped:
	case <-shutdownCtx.Done():
		logger.Warn("shutdown timeout reached while the analysis queue was still draining")
	}
}
