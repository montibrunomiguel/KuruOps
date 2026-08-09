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

	"github.com/argusops/argusops/internal/config"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/events"
	"github.com/argusops/argusops/internal/httpserver"
	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/ingest"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, db.PoolConfig{MaxConns: cfg.DBPoolMaxConns, MinConns: cfg.DBPoolMinConns})
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	httpserver.GetMetrics().SetPool(pool.Pool)

	tagRepo := repository.NewTagRepository()
	tagService := service.NewTagService(pool, tagRepo)
	alertRepo := repository.NewAlertRepository()
	alertService := service.NewAlertService(pool, alertRepo, tagService)

	// eventBroadcaster publishes to the same Postgres NOTIFY channel
	// cmd/api's own Broadcaster listens on -- an alert ingested here reaches
	// any connected browser tab (which is only ever talking to cmd/api),
	// even though this is a different process. See internal/events'
	// package doc comment.
	eventBroadcaster := events.NewBroadcaster(pool.Pool, logger)
	go eventBroadcaster.Start(ctx)
	alertService.EnableEventPublishing(eventBroadcaster.Publish)

	// On-call auto-assign is enabled only here, not in cmd/api -- it's a
	// property of the ingest path (see AlertService.Ingest), not something
	// the analyst-facing API needs to know about.
	onCallShiftService := service.NewOnCallShiftService(pool, repository.NewOnCallShiftRepository(), repository.NewUserRepository(), repository.NewTenantRepository())
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
			logger.Warn("auto-analysis failed", "tenant_id", tenantID, "alert_id", alertID, "error", err)
		}
	})

	webhookRepo := repository.NewWebhookRepository()
	handler := ingest.NewHandler(pool, webhookRepo, alertService, tagService, logger)

	hookLimiter := middleware.NewRateLimiter(pool.Pool, "webhook_ip", 60, time.Minute)

	mux := http.NewServeMux()
	mux.Handle("/healthz", httpserver.HealthCheck(pool.Pool))
	mux.HandleFunc("/metrics", httpserver.MetricsHandler)
	mux.Handle("/hooks", hookLimiter(handler))

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.WrapWithObservability(mux, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("ingest listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
