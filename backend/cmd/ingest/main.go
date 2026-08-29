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
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/config"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/events"
	"github.com/kuruops/kuruops/internal/httpserver"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/ingest"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	otelShutdown, tracer, err := telemetry.Setup(ctx, "kuruops-ingest", cfg.OTelExporterOTLPEndpoint)
	if err != nil {
		logger.Error("telemetry setup failed", "error", err)
		os.Exit(1)
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
	// AlertService.EnableAutoAnalysis. This duplicates a chunk of cmd/api's
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
	aiAnalysisService := service.NewAIAnalysisService(
		pool, repository.NewLLMProviderRepository(), alertRepo, repository.NewIncidentRepository(), secretStore,
		mcpServerRepo, mcpToolService, repository.NewAIAnalysisRunRepository(), aiToolCallRepo,
	)
	mcpToolService.SetOnToolCallResolved(aiAnalysisService.ResumeAnalysisRun)
	alertService.EnableAutoAnalysis(func(tenantID, alertID uuid.UUID) {
		// actorID nil: no human triggered this, see StartAlertAnalysis's doc
		// comment. allowedTags nil: the tag-visibility guard is for a
		// specific analyst's view: this is the system analyzing an alert
		// the instant it exists, before any access-scoping question applies.
		// StartAlertAnalysis itself only blocks for the quick synchronous
		// validation (alert exists, LLM provider configured, nothing else
		// already running) -- the actual LLM call already runs in its own
		// goroutine, so this closure (itself already run via `go
		// s.autoAnalyze(...)`, see AlertService.Ingest) returns quickly
		// either way.
		if err := aiAnalysisService.StartAlertAnalysis(context.Background(), tenantID, alertID, nil, nil); err != nil {
			// "no LLM provider configured" is the expected, common case for
			// a tenant that hasn't set up Settings -> AI Integration -- not
			// worth error-level noise on every single ingested alert.
			if strings.Contains(err.Error(), "no LLM provider configured") {
				logger.Debug("auto-analysis skipped, no LLM provider configured", "tenant_id", tenantID)
				return
			}
			// Same reasoning, the other common case: a provider IS
			// configured but hasn't opted into AutoAnalyzeAllAlerts (the
			// default) -- an analyst clicking "Analyze with AI" is still
			// available, this is just the unattended trigger declining.
			if errors.Is(err, service.ErrAutoAnalysisDisabled) {
				logger.Debug("auto-analysis skipped, disabled for the default llm provider", "tenant_id", tenantID)
				return
			}
			logger.Warn("auto-analysis failed", "tenant_id", tenantID, "alert_id", alertID, "error", err)
		}
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

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.WrapWithObservability(mux, logger, tracer),
		ReadHeaderTimeout: 5 * time.Second,
	}

	safego.Go("ingest.ListenAndServe", func() {
		logger.Info("ingest listening", "addr", cfg.HTTPAddr)
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
}
