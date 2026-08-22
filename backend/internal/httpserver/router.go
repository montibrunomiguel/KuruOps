package httpserver

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/httpserver/middleware"
)

type Options struct {
	AlertHandlers                *handlers.AlertHandlers
	IncidentHandlers             *handlers.IncidentHandlers
	PlaybookHandlers             *handlers.PlaybookHandlers
	DashboardHandlers            *handlers.DashboardHandlers
	WebhookHandlers              *handlers.WebhookHandlers
	FieldMappingTemplateHandlers *handlers.FieldMappingTemplateHandlers
	LLMProviderHandlers          *handlers.LLMProviderHandlers
	MCPServerHandlers            *handlers.MCPServerHandlers
	UserHandlers                 *handlers.UserHandlers
	RoleHandlers                 *handlers.RoleHandlers
	AuthHandlers                 *handlers.AuthHandlers
	AccountHandlers              *handlers.AccountHandlers
	IdentityConfigHandlers       *handlers.IdentityConfigHandlers
	TagHandlers                  *handlers.TagHandlers
	UploadHandlers               *handlers.UploadHandlers
	StorageConfigHandlers        *handlers.StorageConfigHandlers
	SMTPConfigHandlers           *handlers.SMTPConfigHandlers
	SlackConfigHandlers          *handlers.SlackConfigHandlers
	OnCallScheduleHandlers       *handlers.OnCallScheduleHandlers
	IncidentSLAHandlers          *handlers.IncidentSLAHandlers
	EscalationPolicyHandlers     *handlers.EscalationPolicyHandlers
	AuditExportHandlers          *handlers.AuditExportHandlers
	DatabaseMigrationHandlers    *handlers.DatabaseMigrationHandlers
	EventsHandlers               *handlers.EventsHandlers
	// OAuthCallbackHandlers serves every 3rd-party OAuth provider's
	// callback (Google Drive, Slack) -- mounted unauthenticated at
	// /auth/oauth, not under /api/v1 -- see that handler's doc comment.
	OAuthCallbackHandlers *handlers.OAuthCallbackHandlers
	// LoginRateLimiter is built by cmd/api (needs a *pgxpool.Pool, which
	// this package otherwise has no reason to depend on -- see
	// middleware.NewRateLimiter) and applied to /auth below.
	LoginRateLimiter func(http.Handler) http.Handler
	// AuthMiddleware guards /api/v1/**. cmd/api builds this as
	// middleware.JWTAuth(verifier) for AUTH_MODE=jwt/dev (real tokens, real
	// login works), or middleware.DevHeaderAuth for AUTH_MODE=dev-headers
	// (bypasses tokens, local development only) -- see cmd/api/main.go.
	AuthMiddleware func(http.Handler) http.Handler
	// Logger is the base logger request-scoped loggers derive from (see
	// middleware.RequestLogger).
	Logger *slog.Logger
	// HealthCheck serves GET /healthz. Built by cmd/api as
	// httpserver.HealthCheck(pool) (needs a *pgxpool.Pool, same reasoning as
	// LoginRateLimiter above) -- a prebuilt handler rather than a raw Pool
	// field so this package's own tests can wire a fake one without a real
	// database connection.
	HealthCheck http.HandlerFunc
	// HTTPRequestTimeout bounds every /api/v1 request except /events/stream
	// and /settings/database-migration (see config.Config's doc comment) --
	// the zero value would make chimw.Timeout fire immediately on every
	// request, so NewRouter falls back to a sane default rather than
	// trusting every caller to set this.
	HTTPRequestTimeout time.Duration
	// DatabaseMigrationTimeout bounds /settings/database-migration/migrate
	// specifically -- see config.Config.DatabaseMigrationTimeout's doc
	// comment for why this route needs a much larger timeout than every
	// other /api/v1 route.
	DatabaseMigrationTimeout time.Duration
	// Tracer starts one span per request (see middleware.TracingMiddleware).
	// nil (the zero value, what every test call site that doesn't care about
	// tracing passes) falls back to a no-op tracer -- see telemetry.Setup for
	// where a real one comes from.
	Tracer trace.Tracer
}

// defaultHTTPRequestTimeout is used when Options.HTTPRequestTimeout is left
// at the zero value -- see its doc comment.
const defaultHTTPRequestTimeout = 30 * time.Second

// defaultDatabaseMigrationTimeout is used when
// Options.DatabaseMigrationTimeout is left at the zero value -- see its doc
// comment.
const defaultDatabaseMigrationTimeout = 10 * time.Minute

func NewRouter(opts Options) http.Handler {
	if opts.HTTPRequestTimeout <= 0 {
		opts.HTTPRequestTimeout = defaultHTTPRequestTimeout
	}
	if opts.DatabaseMigrationTimeout <= 0 {
		opts.DatabaseMigrationTimeout = defaultDatabaseMigrationTimeout
	}
	if opts.Tracer == nil {
		opts.Tracer = noop.NewTracerProvider().Tracer("argusops")
	}

	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(middleware.TracingMiddleware(opts.Tracer))
	r.Use(middleware.RequestLogger(opts.Logger))
	// ClientIPFromHeader("X-Real-IP"), not the deprecated chimw.RealIP: RealIP
	// also trusts X-Forwarded-For / True-Client-IP, both client-suppliable and
	// therefore spoofable if a caller sends one directly (see GO-2026-5777 /
	// GO-2026-5775). nginx.conf sets X-Real-IP unconditionally on every
	// request (proxy_set_header X-Real-IP $remote_addr), overwriting whatever
	// the client sent, so it's the only header safe to trust here.
	r.Use(chimw.ClientIPFromHeader("X-Real-IP"))
	// Not chimw.Logger: it writes plain-text access-log lines through its own
	// default *log.Logger, not opts.Logger, so every request would emit a
	// non-JSON line alongside the JSON logs every cmd/* binary otherwise
	// produces (slog.NewJSONHandler) -- breaks structured log scraping in a
	// real deployment for no benefit RequestLogger/MetricsMiddleware don't
	// already cover.
	r.Use(chimw.Recoverer)
	r.Use(middleware.SecurityHeaders)
	// Not WrapWithObservability here: chi's own r.Use chain already applies
	// RequestID/RequestLogger/MetricsMiddleware individually, interleaved
	// with SecurityHeaders/Recoverer in a specific order -- WrapWithObservability
	// exists for cmd/worker and cmd/ingest's non-chi routing, which has no
	// equivalent chain to interleave with.
	r.Use(MetricsMiddleware)

	r.Get("/healthz", opts.HealthCheck)
	r.Get("/livez", Livez)
	r.Get("/metrics", MetricsHandler)

	// Login endpoints are unauthenticated by definition -- rate limited to prevent brute force attacks
	r.Route("/auth", func(auth chi.Router) {
		auth.Use(opts.LoginRateLimiter)
		opts.AuthHandlers.Routes(auth)

		// OAuth provider callbacks -- same unauthenticated-top-level-GET
		// shape as /auth's own login routes (and the same threat class:
		// LoginRateLimiter applies here too), just not a login flow
		// themselves. Nested under /auth specifically because
		// frontend/nginx.conf only proxies /api/ and /auth/ to the backend
		// -- see OAuthCallbackHandlers' doc comment.
		auth.Route("/oauth", opts.OAuthCallbackHandlers.Routes)
	})

	r.Route("/api/v1", func(api chi.Router) {
		api.Use(opts.AuthMiddleware)
		// Applies to everything below, including /account itself --
		// RequirePasswordChanged always lets handlers.ChangePasswordPath
		// through by comparing the exact request path, so a caller stuck in
		// the must-change-password state can still reach it.
		api.Use(middleware.RequirePasswordChanged(handlers.ChangePasswordPath))

		// Live alert/incident event stream (see events.Broadcaster) --
		// ungated by resourceAccess for the same reason /dashboard is: a
		// viewer scoped to just one resource type still gets a coherent
		// stream, they just won't act on event types their pages don't show.
		// Registered directly on api, deliberately OUTSIDE the timeout-bound
		// Group below: SSE connections are long-lived by design, so the same
		// request-timeout that protects every other route from a saturated
		// DB pool would instead kill every live-update connection on a
		// schedule.
		api.Get("/events/stream", opts.EventsHandlers.Stream)

		api.Group(func(api chi.Router) {
			// Bounds how long a request can wait on a saturated connection
			// pool (or anything else slow) instead of hanging until the
			// client gives up -- pgx respects context deadlines on
			// Acquire/Query/Exec, so this timeout is what actually turns
			// "blocked forever" into a clean 503-ish failure under load. See
			// config.Config's HTTPRequestTimeout doc comment for the env var.
			api.Use(chimw.Timeout(opts.HTTPRequestTimeout))

			api.Route("/account", opts.AccountHandlers.Routes)

			// Playbooks aren't gated by resourceAccess -- they're shared
			// reference material for both alerts and incidents, not a scoped
			// resource type themselves.
			api.Route("/playbooks", opts.PlaybookHandlers.Routes)

			// Read-only tag catalog, same reasoning as playbooks -- any
			// authenticated user needs to see it to pick from it when tagging an
			// alert/incident. Managing the catalog (create/delete) is
			// admin-only, under /settings/tags below.
			api.Route("/tags", opts.TagHandlers.Routes)

			// Any authenticated user can upload/fetch an attached image (comment
			// or alert-close attachments) -- there's only one tenant, so no
			// per-tenant scoping is needed beyond "must be logged in".
			api.Route("/uploads/images", opts.UploadHandlers.Routes)

			// Minimal {id, name} directory, not the full admin user list -- see
			// UserHandlers.Directory's doc comment for why this needs its own
			// ungated route instead of living under /settings/users.
			api.Get("/users/directory", opts.UserHandlers.Directory)

			// Same reasoning as playbooks: the dashboard blends alert and
			// incident KPIs into one summary, so it isn't gated by
			// resourceAccess either -- a viewer scoped to just one resource type
			// still gets a coherent (if partially zero) KPI card set.
			api.Route("/dashboard", opts.DashboardHandlers.Routes)

			// Unlike /dashboard/stats, Follow-up is a real capability of its own
			// -- a SOC analyst can be granted "followup" without "incidents", so
			// this needs its own gate rather than living under the ungated
			// /dashboard group above.
			api.Group(func(followup chi.Router) {
				followup.Use(middleware.RequireResourceAccess(domain.ResourceCapabilityFollowup))
				followup.Route("/dashboard/followup", opts.DashboardHandlers.FollowupRoutes)
			})

			api.Group(func(alerts chi.Router) {
				alerts.Use(middleware.RequireResourceAccess("alerts"))
				alerts.Route("/alerts", opts.AlertHandlers.Routes)
			})

			api.Group(func(incidents chi.Router) {
				incidents.Use(middleware.RequireResourceAccess("incidents"))
				incidents.Route("/incidents", opts.IncidentHandlers.Routes)
			})

			// Everything under /settings changes shared, tenant-wide
			// configuration (webhooks, LLM/MCP integrations, user roles,
			// identity providers) -- admin-only, regardless of resourceAccess.
			api.Group(func(admin chi.Router) {
				admin.Use(middleware.RequireAdmin())
				admin.Route("/settings/webhooks", opts.WebhookHandlers.Routes)
				admin.Route("/settings/field-mapping-templates", opts.FieldMappingTemplateHandlers.Routes)
				admin.Route("/settings/llm-providers", opts.LLMProviderHandlers.Routes)
				admin.Route("/settings/mcp-servers", opts.MCPServerHandlers.Routes)
				admin.Route("/settings/users", opts.UserHandlers.Routes)
				admin.Route("/settings/roles", opts.RoleHandlers.Routes)
				admin.Route("/settings/identity-providers", opts.IdentityConfigHandlers.Routes)
				admin.Route("/settings/tags", opts.TagHandlers.SettingsRoutes)
				admin.Route("/settings/storage", opts.StorageConfigHandlers.Routes)
				admin.Route("/settings/smtp", opts.SMTPConfigHandlers.Routes)
				// Settings -> Conectores -> Slack: connect/disconnect only
				// (this foundation phase). Nested under /settings/integrations
				// rather than alongside the other /settings/* routes above so
				// future connectors (beyond Slack) share one URL prefix,
				// matching the frontend's separate "Conectores" nav section
				// (kept apart from the existing "Integrações" group).
				admin.Route("/settings/integrations/slack", opts.SlackConfigHandlers.Routes)
				admin.Route("/settings/on-call-schedules", opts.OnCallScheduleHandlers.Routes)
				admin.Route("/settings/incident-sla", opts.IncidentSLAHandlers.Routes)
				admin.Route("/settings/escalation-policies", opts.EscalationPolicyHandlers.Routes)
				admin.Route("/settings/audit-export", opts.AuditExportHandlers.Routes)
			})
		})

		// Settings -> External Database's /migrate can run for minutes on a
		// real dataset (see db_migration.go's doc comment) -- registered
		// directly on api, deliberately OUTSIDE the HTTPRequestTimeout-bound
		// Group above, same reasoning as /events/stream, but with its own
		// much longer timeout (DatabaseMigrationTimeout) rather than none at
		// all: a genuinely hung migration still shouldn't hold a connection
		// open forever.
		api.Group(func(migration chi.Router) {
			migration.Use(middleware.RequireAdmin())
			migration.Use(chimw.Timeout(opts.DatabaseMigrationTimeout))
			migration.Route("/settings/database-migration", opts.DatabaseMigrationHandlers.Routes)
		})
	})

	return r
}
