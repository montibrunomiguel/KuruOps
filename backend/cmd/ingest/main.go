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

	"github.com/argusops/argusops/internal/config"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/ingest"
	"github.com/argusops/argusops/internal/repository"
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

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	tagRepo := repository.NewTagRepository()
	tagService := service.NewTagService(pool, tagRepo)
	alertRepo := repository.NewAlertRepository()
	alertService := service.NewAlertService(pool, alertRepo, tagService)

	// On-call auto-assign is enabled only here, not in cmd/api -- it's a
	// property of the ingest path (see AlertService.Ingest), not something
	// the analyst-facing API needs to know about.
	onCallShiftService := service.NewOnCallShiftService(pool, repository.NewOnCallShiftRepository(), repository.NewUserRepository(), repository.NewTenantRepository())
	alertService.EnableOnCallAutoAssign(onCallShiftService)

	webhookRepo := repository.NewWebhookRepository()
	handler := ingest.NewHandler(pool, webhookRepo, alertService, tagService, logger)

	hookLimiter := middleware.NewRateLimiter(60, time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/hooks", hookLimiter(handler))

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
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
