// Command analyzer works the durable queue of automatic alert analyses: it claims
// the jobs cmd/ingest writes (in the same transaction that stores each alert),
// runs the LLM analysis, and retries or abandons them per the queue's rules.
//
// It used to run inside cmd/ingest. That put the slow, outbound, secret-holding
// half of the work in the process that faces the internet: an LLM call that takes a
// minute shared a process, a pool and a restart with webhook receipt, so a deploy of
// the receiver interrupted analyses and an LLM provider problem could compete with
// alert intake for the same resources. Now ingest only receives and enqueues, and
// this process -- which holds the LLM, MCP and secrets wiring -- scales, restarts
// and is network-policied on its own. Several replicas share one queue safely (jobs
// are claimed with FOR UPDATE SKIP LOCKED and a lease).
//
// It connects as kuruops_app like cmd/api and cmd/ingest, so row-level security
// applies; the queue's own reads use a narrow, named carve-out (the
// app.ai_queue_runner setting) rather than a BYPASSRLS role.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/buildinfo"
	"github.com/kuruops/kuruops/internal/circuitbreaker"
	"github.com/kuruops/kuruops/internal/config"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/events"
	"github.com/kuruops/kuruops/internal/httpserver"
	"github.com/kuruops/kuruops/internal/llmclient"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/safego"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/telemetry"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runMain(ctx, os.Stdout)
	stop()
	os.Exit(code)
}

// runMain is main without the process: it loads and validates the configuration, runs the
// command until ctx is cancelled, and returns the exit status instead of exiting -- so a
// test can drive the whole startup path (including the refusal to start) in-process.
func runMain(ctx context.Context, stdout io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stdout, nil))

	cfg, err := config.LoadAndValidate(logger)
	if err != nil {
		logger.Error("startup refused", "error", err)
		return 1
	}
	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("analyzer stopped", "error", err)
		return 1
	}
	return 0
}

// run wires the process and blocks until ctx is cancelled, then drains the queue.
// It returns an error only for a failure to start; a clean stop returns nil.
func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	otelShutdown, tracer, err := telemetry.Setup(ctx, "kuruops-analyzer", cfg.OTelExporterOTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			logger.Warn("telemetry shutdown failed", "error", err)
		}
	}()

	// Fail fast on an LLM/MCP provider that has gone bad, instead of making every
	// analysis wait out the full HTTP timeout against a dead endpoint; a zero
	// threshold or cooldown disables it -- see config.Config. Owned by this process
	// and handed to the services that make the calls.
	breakerCfg := circuitbreaker.Config{
		Threshold: cfg.ExternalCallBreakerThreshold,
		Cooldown:  cfg.ExternalCallBreakerCooldown,
	}
	llmFactory := llmclient.NewFactory(llmclient.Options{
		Retry:   llmclient.RetryPolicy{MaxAttempts: cfg.LLMCallMaxAttempts, BaseDelay: cfg.LLMCallRetryBaseDelay},
		Breaker: breakerCfg,
	})
	mcpBreakers := circuitbreaker.NewRegistry(breakerCfg)

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, db.PoolConfig{MaxConns: cfg.DBPoolMaxConns, MinConns: cfg.DBPoolMinConns, Tracer: tracer})
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
	}
	defer pool.Close()
	metrics := httpserver.GetMetrics()
	metrics.SetPool(pool.Pool)

	secretStore, err := secrets.NewFromConfig(ctx, cfg, pool)
	if err != nil {
		return fmt.Errorf("secrets backend %q setup: %w", cfg.SecretsBackend, err)
	}

	// The same Postgres NOTIFY channel cmd/api listens on: the end of an analysis
	// (and each model turn) reaches any open browser tab even though this is a
	// different process. It is also how this process hears that a job was just
	// enqueued -- see the tap below.
	eventBroadcaster := events.NewBroadcaster(pool.Pool, logger)
	safego.Go("analyzer.eventBroadcaster", func() { eventBroadcaster.Start(ctx) })

	mcpServerRepo := repository.NewMCPServerRepository()
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolService := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	mcpToolService.SetCircuitBreakers(mcpBreakers)
	aiAnalysisService := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), repository.NewAlertRepository(), repository.NewIncidentRepository(), secretStore,
		mcpServerRepo, mcpToolService, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
		service.WithLLMClientFactory(llmFactory),
	)
	mcpToolService.SetOnToolCallResolved(aiAnalysisService.ResumeAnalysisRun)
	aiAnalysisService.EnableEventPublishing(eventBroadcaster.Publish)

	analysisQueue := service.NewAIAnalysisQueue(pool, repository.NewAIAnalysisJobRepository(), aiAnalysisService, service.AIAnalysisQueueConfig{
		Concurrency:  cfg.AIAnalysisConcurrency,
		PollInterval: cfg.AIAnalysisPollInterval,
		// Leave room inside ShutdownTimeout for the HTTP server to stop too.
		DrainTimeout: max(cfg.ShutdownTimeout-3*time.Second, 5*time.Second),
	}, logger)
	metrics.SetAIQueueStats(analysisQueue.Stats)

	// cmd/ingest publishes an "alert received" event after it commits the alert and
	// its job; waking on it starts the analysis immediately instead of at the next
	// poll. A missed event (this process was restarting, the NOTIFY was lost) costs
	// at most one poll interval, never the job -- the queue is the source of truth.
	eventBroadcaster.Tap(func(_ uuid.UUID, ev events.Event) {
		if wakesQueue(ev) {
			analysisQueue.Wake()
		}
	})

	queueStopped := make(chan struct{})
	safego.Go("analyzer.aiAnalysisQueue", func() {
		defer close(queueStopped)
		analysisQueue.Run(ctx)
	})

	mux := http.NewServeMux()
	mux.Handle("/healthz", httpserver.HealthCheck(pool.Pool))
	mux.HandleFunc("/livez", httpserver.Livez)
	mux.HandleFunc("/metrics", httpserver.MetricsHandler)
	srv := httpserver.NewServer(cfg.HTTPAddr, httpserver.WrapWithObservability(mux, logger, tracer), httpserver.InternalTimeouts)
	serveErr := make(chan error, 1)
	safego.Go("analyzer.ListenAndServe", func() {
		logger.Info("analyzer listening", "addr", cfg.HTTPAddr, "build", buildinfo.Build)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	})

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		return err
	}
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}

	// The queue stopped claiming the moment ctx was cancelled; wait for the analyses
	// it already started to finish (or be handed back, past its drain timeout) so a
	// deploy does not drop them.
	select {
	case <-queueStopped:
	case <-shutdownCtx.Done():
		logger.Warn("shutdown timeout reached while the analysis queue was still draining")
	}
	return nil
}

// wakesQueue reports whether ev says an alert just arrived, which is when a job may
// have been enqueued. cmd/ingest publishes {"type":"alert","payload":{"action":"received"}}.
func wakesQueue(ev events.Event) bool {
	if ev.Type != "alert" {
		return false
	}
	payload, ok := ev.Payload.(map[string]any)
	return ok && payload["action"] == "received"
}
