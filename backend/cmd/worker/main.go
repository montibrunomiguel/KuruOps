// Command worker runs background jobs: refreshing the dashboard's
// materialized views on a schedule, and (once wired up) consuming the AI
// analysis queue — LLM calls and MCP tool invocations for "Analyze with AI".
// Kept separate from cmd/api so an AI provider having a slow day never
// blocks the alert/incident CRUD path.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/config"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/httpserver"
	"github.com/argusops/argusops/internal/mailer"
	"github.com/argusops/argusops/internal/notifier"
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

	// Unlike cmd/api/cmd/ingest, this process has no request traffic of its
	// own to serve -- /healthz and /metrics exist purely so the same
	// container-orchestration probes (docker-compose healthcheck, a future
	// Kubernetes readinessProbe) that already work against api/ingest also
	// work here, instead of a worker replica silently wedged with no way to
	// detect it externally.
	healthMux := http.NewServeMux()
	healthMux.Handle("/healthz", httpserver.HealthCheck(pool.Pool))
	healthMux.HandleFunc("/metrics", httpserver.MetricsHandler)
	healthSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.WrapWithObservability(healthMux, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("worker health/metrics listening", "addr", cfg.HTTPAddr)
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("health server failed", "error", err)
		}
	}()

	// Only needed to resolve an escalation policy's destination
	// (PagerDuty routing key / Slack webhook URL / generic webhook URL) --
	// same SECRETS_BACKEND-driven factory cmd/api uses, see
	// secrets.NewFromConfig.
	secretStore, err := secrets.NewFromConfig(ctx, cfg, pool)
	if err != nil {
		logger.Error("secrets backend setup failed", "backend", cfg.SecretsBackend, "error", err)
		os.Exit(1)
	}

	// Only needed for sweepEscalations' best-effort "also email whoever's on
	// shift" step -- see notifyOnCallAnalyst. Every constructor here is the
	// same one cmd/api uses, just wired to the worker's own BYPASSRLS pool
	// (each call is still scoped to one tenant via pool.WithTenant, same as
	// cmd/api's per-request scoping).
	onCallService := service.NewOnCallShiftService(pool, repository.NewOnCallShiftRepository(), repository.NewUserRepository(), repository.NewTenantRepository())
	userRepo := repository.NewUserRepository()
	smtpService := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secretStore, mailer.SMTPSender{})

	logger.Info("worker started")

	refreshTicker := time.NewTicker(1 * time.Minute)
	defer refreshTicker.Stop()

	slaTicker := time.NewTicker(1 * time.Minute)
	defer slaTicker.Stop()

	escalationTicker := time.NewTicker(1 * time.Minute)
	defer escalationTicker.Stop()

	// Análise por IA na ingestão NÃO passa por este worker -- ficou resolvida
	// de um jeito mais simples do que a fila via `ai_analysis_jobs` que este
	// comentário cogitava originalmente: AlertService.Ingest (cmd/ingest)
	// dispara `go s.autoAnalyze(...)` -- uma goroutine fire-and-forget no
	// próprio processo de ingest, sem fila persistida, sem retry automático
	// se a chamada à LLM falhar (fica só logado). Isso é aceitável para o
	// volume atual; se isso um dia virar gargalo (rajada de alertas
	// derrubando o throughput de ingest, ou precisar de retry/backoff em
	// falha de LLM), uma fila consumida aqui pelo worker (mesmo desenho
	// cogitado abaixo: tabela + `SELECT ... FOR UPDATE SKIP LOCKED`) resolve
	// isso sem tocar em `AlertService.Ingest` de novo.
	//
	// IMPORTANT se isso for implementado: cfg.DatabaseURL aqui conecta como
	// argusops_worker, que tem BYPASSRLS (necessário só para o refresh das
	// materialized views e os sweeps abaixo -- ver
	// db/init/argusops_worker_role.sql). Processar jobs de IA por tenant
	// usando essa MESMA pool seria um bypass silencioso de RLS; abra uma
	// pool separada conectada como argusops_app + Pool.WithTenant para esse
	// consumo, do jeito que api/ingest já fazem.

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
			if err := healthSrv.Shutdown(shutdownCtx); err != nil {
				logger.Error("health server graceful shutdown failed", "error", err)
			}
			cancel()
			return
		case <-refreshTicker.C:
			runLocked(ctx, pool, lockKeyRefreshMaterializedViews, "refresh_materialized_views", logger, func() {
				refreshMaterializedViews(ctx, pool, logger)
			})
		case <-slaTicker.C:
			runLocked(ctx, pool, lockKeySweepSLABreaches, "sweep_sla_breaches", logger, func() {
				sweepSLABreaches(ctx, pool, logger)
			})
		case <-escalationTicker.C:
			runLocked(ctx, pool, lockKeySweepEscalations, "sweep_escalations", logger, func() {
				sweepEscalations(ctx, pool, secretStore, onCallService, userRepo, smtpService, cfg.AppBaseURL, logger)
			})
		}
	}
}

// Arbitrary, distinct Postgres advisory-lock keys, one per periodic job --
// see runLocked. Only required to be unique within this application (no
// other advisory lock use exists anywhere else in the codebase as of this
// writing); if that ever changes, keep this comment updated so a future job
// doesn't accidentally collide with one of these.
const (
	lockKeyRefreshMaterializedViews int64 = 821001
	lockKeySweepSLABreaches         int64 = 821002
	lockKeySweepEscalations         int64 = 821003
)

// runLocked runs fn only if this process wins the Postgres advisory lock
// for key this tick (db.Pool.WithAdvisoryLock) -- if cmd/worker is ever
// scaled to more than one replica, every replica's ticker fires at roughly
// the same time, but only one of them actually runs fn; the others log a
// Debug line and wait for their next tick. Without this, sweepEscalations
// in particular would double-send the same escalation notification (it
// reads candidates, notifies, THEN stamps escalated_at -- two replicas
// racing the same tick could both notify before either stamps).
func runLocked(ctx context.Context, pool *db.Pool, key int64, job string, logger *slog.Logger, fn func()) {
	acquired, err := pool.WithAdvisoryLock(ctx, key, func(context.Context) error {
		fn()
		return nil
	})
	if err != nil {
		logger.Error("advisory lock failed", "job", job, "error", err)
		return
	}
	if !acquired {
		logger.Debug("skipping tick, another replica holds the lock", "job", job)
	}
}

// refreshMaterializedViews recomputes mv_alert_daily_stats, mv_incident_kpis,
// and mv_incident_daily_stats. This intentionally runs outside Pool.WithTenant: the
// views are cross-tenant by definition (grouped by tenant_id) and
// materialized views cannot carry RLS policies in Postgres, so tenant
// isolation for these two views is the API layer's responsibility -- every
// query against them MUST include `where tenant_id = $1`. REFRESH ...
// CONCURRENTLY requires the unique indexes created alongside the views in
// db/migrations/0009_materialized_views.up.sql.
//
// cfg.DatabaseURL must point at argusops_worker (BYPASSRLS), not
// argusops_app: Postgres runs a materialized view's defining query with the
// VIEW OWNER's privileges, not the caller's, so whichever role owns these
// two views also determines the RLS context REFRESH runs under. With no
// tenant context set here (by design, see above), owning them with a
// non-bypass role means their `tenant_id = current_tenant_id()` policy
// matches nothing and every refresh silently produces zero rows -- no
// error, MTTA/MTTR just stay empty forever. See
// db/init/argusops_worker_role.sql.
func refreshMaterializedViews(ctx context.Context, pool *db.Pool, logger *slog.Logger) {
	views := []string{"mv_alert_daily_stats", "mv_incident_kpis", "mv_incident_daily_stats"}
	for _, v := range views {
		if _, err := pool.Exec(ctx, "refresh materialized view concurrently "+v); err != nil {
			logger.Error("refresh materialized view failed", "view", v, "error", err)
		}
	}
}

// sweepSLABreaches flips incidents.sla_breached to true once sla_due_at has
// passed. Runs cross-tenant (no Pool.WithTenant, same reasoning as
// refreshMaterializedViews -- see db/init/argusops_worker_role.sql for why
// this connection is BYPASSRLS) rather than computed at read time: every
// existing read path (Follow-up, dashboard stats, the detail-page badge)
// already treats sla_breached as a plain stored boolean, so a worker sweep
// keeps "breached" a single fact computed once instead of reworking three
// call sites to express "compute against now()".
//
// phase <> 'post_incident' excludes incidents that have already reached
// post-incident -- their SLA clock has effectively stopped, and flipping
// sla_breached retroactively on an incident that's essentially done would
// be misleading, not informative. Never un-flips sla_breached once true,
// even if the incident later reopens: a breach is a historical fact.
func sweepSLABreaches(ctx context.Context, pool *db.Pool, logger *slog.Logger) {
	tag, err := pool.Exec(ctx, `
		update incidents set sla_breached = true, updated_at = now()
		where sla_due_at < now() and sla_breached = false and phase <> 'post_incident'`,
	)
	if err != nil {
		logger.Error("sweep sla breaches failed", "error", err)
		return
	}
	if tag.RowsAffected() > 0 {
		logger.Info("sla breaches flipped", "count", tag.RowsAffected())
	}
}

// escalationCandidate is one open, unacknowledged, un-escalated alert whose
// severity has a configured escalation policy that's now overdue.
type escalationCandidate struct {
	alertID         uuid.UUID
	tenantID        uuid.UUID
	title           string
	severity        string
	channelType     string
	destinationRef  string
	webhookTemplate *string
}

// sweepEscalations fires an on-call notification for every alert that's
// stayed 'open' (never even acknowledged into 'investigating') past its
// severity's escalation_policies.unacknowledged_after_minutes, then stamps
// alerts.escalated_at so it never fires twice for the same alert. Runs
// cross-tenant (no Pool.WithTenant), same BYPASSRLS reasoning as
// refreshMaterializedViews/sweepSLABreaches -- see
// db/init/argusops_worker_role.sql.
//
// A single failed notification (unreachable PagerDuty/Slack, a bad
// destination secret) is logged and skipped, not retried here -- the alert
// stays un-escalated (escalated_at stays null), so the very next sweep
// tick will simply try it again.
//
// Beyond the configured channel, this also makes a best-effort attempt to
// email whoever's actually on shift right now (see notifyOnCallAnalyst) --
// additive only: no on-call analyst resolved, or no SMTP configured for the
// tenant, never blocks or fails the primary escalation.
func sweepEscalations(ctx context.Context, pool *db.Pool, secretStore secrets.Store, onCall *service.OnCallShiftService, users *repository.UserRepository, smtp *service.SMTPConfigService, appBaseURL string, logger *slog.Logger) {
	rows, err := pool.Query(ctx, `
		select a.id, a.tenant_id, a.title, a.severity::text, ep.channel_type, ep.destination_secret_ref, ep.webhook_payload_template
		from alerts a
		join escalation_policies ep on ep.tenant_id = a.tenant_id and ep.severity = a.severity
		where a.status = 'open'
		  and a.escalated_at is null
		  and a.received_at < now() - (ep.unacknowledged_after_minutes || ' minutes')::interval`,
	)
	if err != nil {
		logger.Error("query escalation candidates failed", "error", err)
		return
	}
	var candidates []escalationCandidate
	for rows.Next() {
		var c escalationCandidate
		if err := rows.Scan(&c.alertID, &c.tenantID, &c.title, &c.severity, &c.channelType, &c.destinationRef, &c.webhookTemplate); err != nil {
			logger.Error("scan escalation candidate failed", "error", err)
			continue
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		logger.Error("iterate escalation candidates failed", "error", err)
		return
	}

	for _, c := range candidates {
		sender, err := notifier.NewForPolicy(c.channelType, c.webhookTemplate)
		if err != nil {
			logger.Error("unknown escalation channel", "alert_id", c.alertID, "channel", c.channelType, "error", err)
			continue
		}
		destination, err := secretStore.Resolve(ctx, c.destinationRef)
		if err != nil {
			logger.Error("resolve escalation destination failed", "alert_id", c.alertID, "error", err)
			continue
		}

		notification := notifier.Notification{
			Title: c.title, Severity: c.severity, AlertID: c.alertID.String(),
			URL: appBaseURL + "/alerts/" + c.alertID.String(),
		}
		err = sender.Send(ctx, destination, notification)
		if err != nil {
			logger.Error("send escalation notification failed", "alert_id", c.alertID, "channel", c.channelType, "error", err)
			continue
		}

		if err := notifyOnCallAnalyst(ctx, pool, onCall, users, smtp, c, appBaseURL); err != nil {
			logger.Warn("on-call analyst email skipped", "alert_id", c.alertID, "error", err)
		}

		if _, err := pool.Exec(ctx, `update alerts set escalated_at = now() where id = $1`, c.alertID); err != nil {
			logger.Error("stamp escalated_at failed", "alert_id", c.alertID, "error", err)
			continue
		}
		logger.Info("alert escalated", "alert_id", c.alertID, "channel", c.channelType)
	}
}

// notifyOnCallAnalyst resolves whoever's on shift for c.tenantID right now
// and, if SMTP is configured for that tenant, emails them -- the escalation
// policy's own channel (PagerDuty/Slack/webhook) is a fixed external
// destination that has no idea who's actually on the schedule; this closes
// that gap without changing what the configured channel does. Returns a
// non-nil error only to describe why nothing was sent (no shift covers
// right now, no SMTP configured, delivery failed) -- callers treat every
// case as best-effort, never a reason to fail the escalation itself.
func notifyOnCallAnalyst(ctx context.Context, pool *db.Pool, onCall *service.OnCallShiftService, users *repository.UserRepository, smtp *service.SMTPConfigService, c escalationCandidate, appBaseURL string) error {
	analystID, err := onCall.ResolveCurrentAnalyst(ctx, c.tenantID, time.Now())
	if err != nil {
		return fmt.Errorf("resolve on-call analyst: %w", err)
	}
	if analystID == nil {
		return fmt.Errorf("no analyst currently on shift")
	}

	var analystEmail string
	err = pool.WithTenant(ctx, c.tenantID, func(tx pgx.Tx) error {
		u, err := users.Get(ctx, tx, *analystID)
		if err != nil {
			return err
		}
		if u == nil {
			return fmt.Errorf("on-call analyst %s not found", *analystID)
		}
		analystEmail = u.Email
		return nil
	})
	if err != nil {
		return fmt.Errorf("load on-call analyst: %w", err)
	}

	msg := mailer.Message{
		To:      analystEmail,
		Subject: fmt.Sprintf("[ArgusOps] Alerta escalado: %s", c.title),
		Body: fmt.Sprintf(
			"O alerta \"%s\" (severidade %s) ficou sem reconhecimento além do tempo configurado para escalonamento e você está de plantão agora.\n\n%s/alerts/%s",
			c.title, c.severity, appBaseURL, c.alertID,
		),
	}
	if err := smtp.Send(ctx, c.tenantID, msg); err != nil {
		return fmt.Errorf("send on-call email: %w", err)
	}
	return nil
}
