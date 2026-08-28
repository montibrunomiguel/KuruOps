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

	"github.com/kuruops/kuruops/internal/config"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver"
	"github.com/kuruops/kuruops/internal/mailer"
	"github.com/kuruops/kuruops/internal/notifier"
	"github.com/kuruops/kuruops/internal/repository"
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

	otelShutdown, tracer, err := telemetry.Setup(ctx, "kuruops-worker", cfg.OTelExporterOTLPEndpoint)
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

	// Unlike cmd/api/cmd/ingest, this process has no request traffic of its
	// own to serve -- /healthz and /metrics exist purely so the same
	// container-orchestration probes (docker-compose healthcheck, a future
	// Kubernetes readinessProbe) that already work against api/ingest also
	// work here, instead of a worker replica silently wedged with no way to
	// detect it externally.
	healthMux := http.NewServeMux()
	healthMux.Handle("/healthz", httpserver.HealthCheck(pool.Pool))
	healthMux.HandleFunc("/livez", httpserver.Livez)
	healthMux.HandleFunc("/metrics", httpserver.MetricsHandler)
	healthSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.WrapWithObservability(healthMux, logger, tracer),
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

	// Every constructor here is the same one cmd/api uses, just wired to the
	// worker's own BYPASSRLS pool (each call is still scoped to one tenant
	// via pool.WithTenant, same as cmd/api's per-request scoping).
	userRepo := repository.NewUserRepository()
	userService := service.NewUserService(pool, userRepo, repository.NewAdminAuditEventRepository())
	onCallScheduleRepo := repository.NewOnCallScheduleRepository()
	onCallService := service.NewOnCallScheduleService(pool, onCallScheduleRepo, userRepo, repository.NewTenantRepository(), repository.NewAdminAuditEventRepository())
	// escalationPolicyService.ResolveStepNotification is sweepEscalations'
	// bridge from "which step, on which schedule" to a ready-to-send
	// notifier.Notification -- resolves the on-call analyst for that
	// specific step's schedule (not necessarily the tenant's default) and
	// their contact info, same helper AlertHandlers.escalate uses for a
	// manual escalation.
	escalationPolicyService := service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), onCallScheduleRepo, onCallService, userService, secretStore, repository.NewAdminAuditEventRepository())
	smtpService := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secretStore, mailer.SMTPSender{}, repository.NewAdminAuditEventRepository())

	logger.Info("worker started")

	refreshTicker := time.NewTicker(1 * time.Minute)
	defer refreshTicker.Stop()

	slaTicker := time.NewTicker(1 * time.Minute)
	defer slaTicker.Stop()

	escalationTicker := time.NewTicker(1 * time.Minute)
	defer escalationTicker.Stop()

	staleAIRunTicker := time.NewTicker(1 * time.Minute)
	defer staleAIRunTicker.Stop()

	// Coarser than the other sweeps on purpose -- deleting data has no
	// timeliness requirement the way an SLA breach or an escalation firing
	// does, so ticking every minute would just be wasted table scans. See
	// sweepDataRetention's own doc comment.
	retentionTicker := time.NewTicker(1 * time.Hour)
	defer retentionTicker.Stop()

	// AI analysis on ingest does NOT go through this worker -- it ended up
	// solved a simpler way than the `ai_analysis_jobs` queue this comment
	// originally considered: AlertService.Ingest (cmd/ingest) fires
	// `go s.autoAnalyze(...)` -- a fire-and-forget goroutine in the ingest
	// process itself, no persisted queue, no automatic retry if the LLM call
	// fails (just logged). That's acceptable at current volume; if this ever
	// becomes a bottleneck (a burst of alerts dragging down ingest
	// throughput, or needing retry/backoff on an LLM failure), a queue
	// consumed here by the worker (same design considered below: a table +
	// `SELECT ... FOR UPDATE SKIP LOCKED`) would solve it without touching
	// `AlertService.Ingest` again.
	//
	// IMPORTANT if this ever gets built: cfg.DatabaseURL here connects as
	// kuruops_worker, which has BYPASSRLS (needed only for the materialized
	// view refresh and the sweeps below -- see
	// db/init/kuruops_worker_role.sql). Processing AI jobs per-tenant using
	// this SAME pool would be a silent RLS bypass; open a separate pool
	// connected as kuruops_app + Pool.WithTenant for that consumption, the
	// way api/ingest already do.

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
				sweepEscalations(ctx, pool, escalationPolicyService, smtpService, cfg.AppBaseURL, logger)
			})
		case <-staleAIRunTicker.C:
			runLocked(ctx, pool, lockKeySweepStaleAIRuns, "sweep_stale_ai_runs", logger, func() {
				sweepStaleAIRuns(ctx, pool, logger)
			})
		case <-retentionTicker.C:
			runLocked(ctx, pool, lockKeySweepDataRetention, "sweep_data_retention", logger, func() {
				sweepDataRetention(ctx, pool, logger)
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
	lockKeySweepStaleAIRuns         int64 = 821004
	lockKeySweepDataRetention       int64 = 821005
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
		return
	}
	// fn returned without panicking (a panic here would already have
	// propagated out of WithAdvisoryLock, past this line) -- see
	// MetricsCollector.RecordSweepSuccess's doc comment for what an
	// Alertmanager rule watching this gauge is actually meant to catch.
	httpserver.GetMetrics().RecordSweepSuccess(job)
}

// refreshMaterializedViews recomputes mv_alert_daily_stats, mv_incident_kpis,
// and mv_incident_daily_stats. This intentionally runs outside Pool.WithTenant: the
// views are cross-tenant by definition (grouped by tenant_id) and
// materialized views cannot carry RLS policies in Postgres, so tenant
// isolation for these two views is the API layer's responsibility -- every
// query against them MUST include `where tenant_id = $1`. REFRESH ...
// CONCURRENTLY requires the unique indexes created alongside the views in
// db/migrations/0001_initial_schema.up.sql.
//
// cfg.DatabaseURL must point at kuruops_worker (BYPASSRLS), not
// kuruops_app: Postgres runs a materialized view's defining query with the
// VIEW OWNER's privileges, not the caller's, so whichever role owns these
// two views also determines the RLS context REFRESH runs under. With no
// tenant context set here (by design, see above), owning them with a
// non-bypass role means their `tenant_id = current_tenant_id()` policy
// matches nothing and every refresh silently produces zero rows -- no
// error, MTTA/MTTR just stay empty forever. See
// db/init/kuruops_worker_role.sql.
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
// refreshMaterializedViews -- see db/init/kuruops_worker_role.sql for why
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

// staleAIRunTimeout is how long an ai_analysis_runs row is allowed to sit at
// 'running'/'paused' before sweepStaleAIRuns reaps it -- generous enough for
// a real LLM round-trip (including the agentic tool-use loop's wait for a
// human to approve/reject a tool call) but short enough that an analyst
// isn't stuck behind a permanently "in progress" state for long.
const staleAIRunTimeout = 15 * time.Minute

// sweepStaleAIRuns fails any ai_analysis_runs row still 'running'/'paused'
// well past when it should have finished -- StartAlertAnalysis/
// StartIncidentAnalysis (see AIAnalysisService) do the actual LLM work in a
// goroutine started with context.Background(), fully decoupled from both
// the originating HTTP request and process shutdown. If the pod is killed
// mid-goroutine (a routine deploy, OOM, a node eviction), that goroutine
// just dies -- nothing else ever transitions the row again, and
// checkNotAlreadyRunning then permanently blocks any future "Analyze with
// AI" click for that alert/incident. Runs cross-tenant (no Pool.WithTenant),
// same BYPASSRLS reasoning as sweepSLABreaches/sweepEscalations -- see
// db/init/kuruops_worker_role.sql. updated_at is already bumped by every
// real status transition (SetRunning/SetPaused/SetCompleted/SetFailed, see
// AIAnalysisRunRepository), so no schema change is needed to detect
// staleness from it.
func sweepStaleAIRuns(ctx context.Context, pool *db.Pool, logger *slog.Logger) {
	// $1 * interval '1 second', not $1::interval -- pgx has no direct
	// encoding for Go's time.Duration as a Postgres interval, and
	// Duration.String()'s Go-style format ("15m0s") isn't valid interval
	// input anyway. Seconds-as-float multiplied by a one-second interval is
	// the standard way to pass a Go duration through as a bind parameter.
	tag, err := pool.Exec(ctx, `
		update ai_analysis_runs
		set status = 'failed', error = 'reaped: run exceeded timeout', updated_at = now()
		where status in ('running', 'paused') and updated_at < now() - ($1 * interval '1 second')`,
		staleAIRunTimeout.Seconds(),
	)
	if err != nil {
		logger.Error("sweep stale ai runs failed", "error", err)
		return
	}
	if tag.RowsAffected() > 0 {
		logger.Warn("reaped stale ai analysis runs", "count", tag.RowsAffected())
	}
}

// escalationCandidate is one open, unacknowledged, un-escalated alert whose
// severity has a configured escalation policy that's now overdue.
type escalationCandidate struct {
	alertID    uuid.UUID
	tenantID   uuid.UUID
	title      string
	severity   string
	receivedAt time.Time
	// slaStep is alerts.sla_escalation_step as it currently reads -- the
	// automatic loop's own counter, entirely independent of
	// manual_escalation_step (see AlertHandlers.escalate). Never reset,
	// wrapped via `% len(steps)` at read time instead, so the stored value
	// simply keeps counting up across the alert's whole open lifetime.
	slaStep     int
	escalatedAt *time.Time
}

type escalationStepRow struct {
	scheduleID      uuid.UUID
	delayMinutes    int
	channelType     string
	destinationRef  string
	webhookTemplate *string
}

// sweepEscalations advances every open/investigating alert's automatic SLA
// escalation loop: for each alert whose severity has a configured chain,
// finds the step its sla_escalation_step currently points at (wrapping back
// to the first step once the chain is exhausted -- `% len(steps)`, "roda as
// escalas até ser atendido"), and fires it once the configured delay since
// the previous event (the alert opening, for step 0; the last automatic
// fire, for later steps) has elapsed. Runs cross-tenant (no Pool.WithTenant
// at the query level -- escalationPolicyService's own calls scope
// themselves per alert), same BYPASSRLS reasoning as
// refreshMaterializedViews/sweepSLABreaches -- see
// db/init/kuruops_worker_role.sql.
//
// A single failed Send (already retried with backoff inside
// notifier.RetryingSender -- see internal/notifier/retry.go) is logged and
// skipped, not retried across sweep ticks here -- sla_escalation_step/
// escalated_at are only advanced after Send succeeds, so the very next tick
// simply tries the same step again.
//
// This loop is entirely independent of AlertHandlers.escalate's manual
// escalation path (see alerts.manual_escalation_step) -- a human escalating
// an alert never advances sla_escalation_step, and this sweep never
// advances manual_escalation_step.
func sweepEscalations(ctx context.Context, pool *db.Pool, escalationPolicies *service.EscalationPolicyService, smtp *service.SMTPConfigService, appBaseURL string, logger *slog.Logger) {
	rows, err := pool.Query(ctx, `
		select a.id, a.tenant_id, a.title, a.severity::text, a.received_at, a.sla_escalation_step, a.escalated_at
		from alerts a
		where a.status in ('open', 'investigating')
		  and exists (
		    select 1 from escalation_policies ep
		    join escalation_policy_steps s on s.policy_id = ep.id
		    where ep.tenant_id = a.tenant_id and ep.severity = a.severity
		  )`,
	)
	if err != nil {
		logger.Error("query escalation candidates failed", "error", err)
		return
	}
	var candidates []escalationCandidate
	for rows.Next() {
		var c escalationCandidate
		if err := rows.Scan(&c.alertID, &c.tenantID, &c.title, &c.severity, &c.receivedAt, &c.slaStep, &c.escalatedAt); err != nil {
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

	// Cached per (tenant, severity) within this tick -- many candidates
	// commonly share the same chain (e.g. every open Critical alert across
	// a tenant), so this avoids re-querying the same steps once per alert.
	stepsCache := map[string][]escalationStepRow{}

	for _, c := range candidates {
		key := c.tenantID.String() + "|" + c.severity
		steps, ok := stepsCache[key]
		if !ok {
			steps, err = loadEscalationSteps(ctx, pool, c.tenantID, c.severity)
			if err != nil {
				logger.Error("load escalation steps failed", "alert_id", c.alertID, "error", err)
				continue
			}
			stepsCache[key] = steps
		}
		if len(steps) == 0 {
			continue
		}

		idx := c.slaStep % len(steps)
		step := steps[idx]

		baseline := c.receivedAt
		if c.slaStep > 0 && c.escalatedAt != nil {
			baseline = *c.escalatedAt
		}
		if time.Now().Before(baseline.Add(time.Duration(step.delayMinutes) * time.Minute)) {
			continue
		}

		sender, err := notifier.NewForPolicy(step.channelType, step.webhookTemplate)
		if err != nil {
			logger.Error("unknown escalation channel", "alert_id", c.alertID, "channel", step.channelType, "error", err)
			continue
		}

		notification, destination, err := escalationPolicies.ResolveStepNotification(ctx, c.tenantID, domain.EscalationStep{
			ScheduleID: step.scheduleID, ChannelType: domain.EscalationChannelType(step.channelType),
			DestinationSecretRef: step.destinationRef, WebhookPayloadTemplate: step.webhookTemplate,
		}, notifier.Notification{
			Title: c.title, Severity: c.severity, AlertID: c.alertID.String(),
			URL: appBaseURL + "/alerts/" + c.alertID.String(),
		})
		if err != nil {
			logger.Error("resolve escalation step notification failed", "alert_id", c.alertID, "error", err)
			continue
		}

		if err := sender.Send(ctx, destination, notification); err != nil {
			logger.Error("send escalation notification failed", "alert_id", c.alertID, "channel", step.channelType, "error", err)
			continue
		}

		next := c.slaStep + 1
		if _, err := pool.Exec(ctx, `update alerts set sla_escalation_step = $2, escalated_at = now() where id = $1`, c.alertID, next); err != nil {
			logger.Error("stamp sla_escalation_step failed", "alert_id", c.alertID, "error", err)
			continue
		}
		logger.Info("alert escalation step fired", "alert_id", c.alertID, "step", idx, "channel", step.channelType)

		// Beyond the step's configured channel, also make a best-effort
		// attempt to email the analyst ResolveStepNotification resolved for
		// this step's schedule -- additive only: no analyst resolved (empty
		// AnalystEmail), or no SMTP configured for the tenant, never blocks
		// or fails the primary escalation.
		if notification.AnalystEmail != "" {
			if err := emailOnCallAnalyst(ctx, smtp, c.tenantID, notification.AnalystEmail, c.title, c.severity, c.alertID, appBaseURL); err != nil {
				logger.Warn("on-call analyst email skipped", "alert_id", c.alertID, "error", err)
			}
		}
	}
}

func loadEscalationSteps(ctx context.Context, pool *db.Pool, tenantID uuid.UUID, severity string) ([]escalationStepRow, error) {
	rows, err := pool.Query(ctx, `
		select s.schedule_id, s.delay_minutes, s.channel_type, s.destination_secret_ref, s.webhook_payload_template
		from escalation_policy_steps s
		join escalation_policies ep on ep.id = s.policy_id
		where ep.tenant_id = $1 and ep.severity = $2
		order by s.position asc`,
		tenantID, severity,
	)
	if err != nil {
		return nil, fmt.Errorf("query escalation steps: %w", err)
	}
	defer rows.Close()

	var steps []escalationStepRow
	for rows.Next() {
		var s escalationStepRow
		if err := rows.Scan(&s.scheduleID, &s.delayMinutes, &s.channelType, &s.destinationRef, &s.webhookTemplate); err != nil {
			return nil, fmt.Errorf("scan escalation step: %w", err)
		}
		steps = append(steps, s)
	}
	return steps, rows.Err()
}

// emailOnCallAnalyst sends a best-effort email to analystEmail (already
// resolved by EscalationPolicyService.ResolveStepNotification against the
// firing step's own schedule) if SMTP is configured for tenantID -- the
// step's own channel (PagerDuty/Slack/webhook) is a fixed external
// destination that has no idea who's actually on the schedule; this closes
// that gap without changing what the configured channel does.
func emailOnCallAnalyst(ctx context.Context, smtp *service.SMTPConfigService, tenantID uuid.UUID, analystEmail, title, severity string, alertID uuid.UUID, appBaseURL string) error {
	msg := mailer.Message{
		To:      analystEmail,
		Subject: fmt.Sprintf("[KuruOps] Alerta escalado: %s", title),
		Body: fmt.Sprintf(
			"O alerta \"%s\" (severidade %s) ficou sem reconhecimento além do tempo configurado para escalonamento e você está de plantão agora.\n\n%s/alerts/%s",
			title, severity, appBaseURL, alertID,
		),
	}
	if err := smtp.Send(ctx, tenantID, msg); err != nil {
		return fmt.Errorf("send on-call email: %w", err)
	}
	return nil
}

// retentionSweepBatchLimit bounds how many alerts (and, separately, how many
// incidents) a single sweepDataRetention tick will purge. Without this, a
// tenant with a large backlog of old closed records on first deploy of this
// feature (exactly the scenario retention exists to clear out) would hold
// one very long transaction locking however many rows it found, competing
// with live traffic for the same pages. A backlog bigger than this limit
// just gets worked off incrementally, one batch per hourly tick, instead of
// in a single unbounded transaction.
const retentionSweepBatchLimit = 5000

// sweepDataRetention permanently deletes closed alerts/incidents once their
// tenant's configured retention period (Settings -> Retention,
// tenant_retention_config; domain.DefaultRetentionMonths if unconfigured)
// has elapsed since they closed. Only ever considers CLOSED
// alerts/incidents -- an open one is never touched no matter how old, since
// eligibility is anchored on closed_at IS NOT NULL, not received_at/
// opened_at.
//
// Deliberately never touches blobstore -- evidence attachments (alert/
// incident comment images) are referenced by URL/key from columns like
// alert_comments.attachment_url, but internal/blobstore.Store has no
// Delete method anywhere in this codebase, so there is nothing to call even
// if this wanted to; a purge here can only ever remove the DB's own record
// of an alert/incident, never anything in whichever storage backend the
// tenant has configured.
//
// Runs cross-tenant (BYPASSRLS kuruops_worker, no Pool.WithTenant, same
// reasoning as the other sweeps -- see db/init/kuruops_worker_role.sql),
// but unlike them, wrapped in its own explicit transaction: this sweep is
// multi-statement and order-dependent (see below), so atomicity actually
// matters here in a way it doesn't for the other sweeps' single UPDATE
// statements.
//
// Eligibility is re-checked atomically at delete time, not just at select
// time: deleteEligibleAlerts/deleteEligibleIncidents each run their
// candidate-selection and their DELETE as ONE SQL statement (a `WITH ...
// FOR UPDATE` CTE feeding the DELETE's `WHERE id IN (...)`), not two
// separate statements. This matters because an incident CAN be reopened
// after closing (IncidentRepository.UpdatePhase clears closed_at the
// instant phase moves off post_incident) -- a naive "SELECT doomed ids,
// then DELETE ... WHERE id = ANY(ids)" as two separate statements has a
// real window between them where a reopen can commit and the second
// statement, which never re-checks closed_at, purges the now-active
// incident anyway. Postgres re-validates FOR UPDATE's WHERE clause against
// each row's current committed values before returning it (EvalPlanQual),
// so a row that stopped matching mid-statement is correctly excluded --
// something two separate statements in the same transaction do NOT get for
// free under READ COMMITTED, which re-reads current state at the start of
// each new statement, not each new row.
func sweepDataRetention(ctx context.Context, pool *db.Pool, logger *slog.Logger) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		logger.Error("sweep data retention: begin tx failed", "error", err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	alertIDs, err := deleteEligibleAlerts(ctx, tx)
	if err != nil {
		logger.Error("sweep data retention: delete alerts failed", "error", err)
		return
	}
	incidentIDs, err := deleteEligibleIncidents(ctx, tx)
	if err != nil {
		logger.Error("sweep data retention: delete incidents failed", "error", err)
		return
	}
	if len(alertIDs) == 0 && len(incidentIDs) == 0 {
		return
	}

	// ai_analysis_runs and ai_tool_calls reference their alert/incident via
	// a polymorphic context_type/context_id pair with NO foreign key back
	// to alerts/incidents at all (see db/migrations/0001_initial_schema.up.sql)
	// -- deleting alerts/incidents above does not (and cannot) cascade into
	// these, so they must be cleared explicitly here, against the IDs
	// actually deleted above (not a stale candidate list). ai_analysis_runs
	// before ai_tool_calls specifically: ai_analysis_runs.pending_tool_call_id
	// has a real FK to ai_tool_calls(id) with no ON DELETE clause (default
	// RESTRICT), so deleting ai_tool_calls first would fail with a
	// foreign-key violation on any run still pointing at one. Every other
	// child table (alert_comments, alert_events, incident_comments,
	// incident_events, ...) already has real ON DELETE CASCADE and needed
	// no explicit statement -- it went with the alerts/incidents delete
	// above; alerts.incident_id is ON DELETE SET NULL, so purging an
	// incident correctly just unlinked any still-open alert referencing it,
	// rather than touching the alert itself.
	if _, err := tx.Exec(ctx, `
		delete from ai_analysis_runs
		where (context_type = 'alert' and context_id = any($1))
		   or (context_type = 'incident' and context_id = any($2))`,
		alertIDs, incidentIDs,
	); err != nil {
		logger.Error("sweep data retention: delete ai_analysis_runs failed", "error", err)
		return
	}
	if _, err := tx.Exec(ctx, `
		delete from ai_tool_calls
		where (context_type = 'alert' and context_id = any($1))
		   or (context_type = 'incident' and context_id = any($2))`,
		alertIDs, incidentIDs,
	); err != nil {
		logger.Error("sweep data retention: delete ai_tool_calls failed", "error", err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		logger.Error("sweep data retention: commit failed", "error", err)
		return
	}
	logger.Info("data retention sweep purged records",
		"alerts_deleted", len(alertIDs), "incidents_deleted", len(incidentIDs))
}

// deleteEligibleAlerts atomically selects (locking, up to
// retentionSweepBatchLimit rows, re-validated against the WHERE clause
// after locking) and deletes closed alerts past their tenant's configured
// retention -- see sweepDataRetention's doc comment for why this has to be
// one statement, not select-then-delete. Alerts have no reopen path
// (AlertService.ChangeStatus refuses to transition a closed alert to
// anything else), so this atomicity is defense-in-depth here, not closing
// a reachable gap the way it does for incidents below.
func deleteEligibleAlerts(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		with doomed as (
			select a.id
			from alerts a
			left join tenant_retention_config trc on trc.tenant_id = a.tenant_id
			where a.status = 'closed' and a.closed_at is not null
			  and a.closed_at < now() - (coalesce(trc.alert_retention_months, $1) || ' months')::interval
			order by a.id
			limit $2
			for update of a
		)
		delete from alerts where id in (select id from doomed)
		returning id`,
		domain.DefaultRetentionMonths, retentionSweepBatchLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("delete eligible alerts: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan deleted alert id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// deleteEligibleIncidents mirrors deleteEligibleAlerts -- see
// sweepDataRetention's doc comment for why re-validating eligibility at
// delete time (not just select time) matters here specifically: unlike
// alerts, an incident CAN be reopened after closing, which clears
// closed_at. If a reopen commits while this statement is waiting on that
// row's lock, Postgres re-checks closed_at IS NOT NULL against the
// now-current row before including it in `doomed`, so a just-reopened
// incident is correctly excluded rather than purged anyway.
func deleteEligibleIncidents(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		with doomed as (
			select i.id
			from incidents i
			left join tenant_retention_config trc on trc.tenant_id = i.tenant_id
			where i.closed_at is not null
			  and i.closed_at < now() - (coalesce(trc.incident_retention_months, $1) || ' months')::interval
			order by i.id
			limit $2
			for update of i
		)
		delete from incidents where id in (select id from doomed)
		returning id`,
		domain.DefaultRetentionMonths, retentionSweepBatchLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("delete eligible incidents: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan deleted incident id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
