// Command ingest is the only service that accepts inbound webhook traffic
// from SIEM/XDR sources. It is kept separate from cmd/api so it can be
// scaled, rate-limited, and network-policied independently of analyst-facing
// traffic — a burst of alerts from a source should never be able to starve
// the dashboard.
//
// It only receives and records: each alert is stored together with a job row
// owing it an automatic analysis (see AlertService.EnableAutoAnalysisQueue), and
// cmd/analyzer does the slow, outbound LLM work. That keeps this internet-facing
// process free of LLM, MCP and secret-store wiring and of any outbound dependency
// on a model provider.
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

	"github.com/kuruops/kuruops/internal/buildinfo"
	"github.com/kuruops/kuruops/internal/config"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/events"
	"github.com/kuruops/kuruops/internal/httpserver"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/ingest"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/safego"
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
		logger.Error("ingest stopped", "error", err)
		return 1
	}
	return 0
}

// run wires the process and blocks until ctx is cancelled, then shuts the server
// down. It returns an error only for a failure to start or serve.
func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	otelShutdown, tracer, err := telemetry.Setup(ctx, "kuruops-ingest", cfg.OTelExporterOTLPEndpoint)
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

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, db.PoolConfig{MaxConns: cfg.DBPoolMaxConns, MinConns: cfg.DBPoolMinConns, Tracer: tracer})
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
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
	// even though this is a different process. cmd/analyzer hears the same event
	// and starts the new alert's analysis at once. See internal/events' package
	// doc comment.
	eventBroadcaster := events.NewBroadcaster(pool.Pool, logger)
	safego.Go("ingest.eventBroadcaster", func() { eventBroadcaster.Start(ctx) })
	alertService.EnableEventPublishing(eventBroadcaster.Publish)

	// On-call auto-assign is enabled only here, not in cmd/api -- it's a
	// property of the ingest path (see AlertService.Ingest), not something
	// the analyst-facing API needs to know about.
	onCallShiftService := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository(), repository.NewAdminAuditEventRepository())
	alertService.EnableOnCallAutoAssign(onCallShiftService)

	// The automatic analysis of each ingested alert is a durable queue: Ingest writes
	// a job row in the alert's own transaction, and cmd/analyzer works the rows with a
	// lease, retry and backoff -- so a restart, a deploy or a provider outage no longer
	// loses the analysis. There is nothing to wake in this process.
	alertService.EnableAutoAnalysisQueue(repository.NewAIAnalysisJobRepository(), nil)

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

	serveErr := make(chan error, 1)
	safego.Go("ingest.ListenAndServe", func() {
		logger.Info("ingest listening", "addr", cfg.HTTPAddr, "build", buildinfo.Build)
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
	return nil
}
