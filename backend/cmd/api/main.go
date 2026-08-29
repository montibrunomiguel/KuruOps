// Command api serves the REST API consumed by the frontend: alerts,
// incidents, playbooks, settings. It does not accept webhook traffic
// (see cmd/ingest) and does not run background jobs (see cmd/worker) —
// kept separate so each can scale and fail independently in Kubernetes.
// It is also the only service that issues session tokens: local/LDAP/SAML
// login all live here (internal/httpserver/handlers/auth.go).
package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/config"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/dbmigrate"
	"github.com/kuruops/kuruops/internal/events"
	"github.com/kuruops/kuruops/internal/httpserver"
	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/mailer"
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

	// AUTH_MODE has three values, two independent concerns:
	//   - "jwt" (default): real JWT_PRIVATE_KEY_PATH/JWT_PUBLIC_KEY_PATH required, JWTAuth enforced.
	//   - "dev": no key files needed (a keypair is generated once and persisted
	//     under cfg.DevKeysDir -- see loadOrGenerateJWTKeys -- so it survives
	//     restarts instead of invalidating every session on every deploy), but
	//     auth is still real -- login still issues a token that JWTAuth
	//     actually verifies. This is what local/docker-compose dev should use:
	//     it lets the frontend's real login flow work without pre-generating
	//     keys.
	//   - "dev-headers": same ephemeral-key convenience as "dev", PLUS swaps in
	//     DevHeaderAuth, which trusts X-Tenant-ID/X-User-ID headers verbatim and
	//     ignores the bearer token entirely. Only useful for curling /api/v1
	//     directly without going through login first; a token from /auth/.../login
	//     will NOT authenticate against this mode, since DevHeaderAuth never looks
	//     at it. Never run this outside a developer's own machine.
	allowEphemeralKeys := cfg.AuthMode == "dev" || cfg.AuthMode == "dev-headers"
	useDevHeaderAuth := cfg.AuthMode == "dev-headers"
	if useDevHeaderAuth {
		logger.Warn("AUTH_MODE=dev-headers: /api/v1 trusts X-Tenant-ID/X-User-ID headers verbatim and ignores bearer tokens, do not run this outside local development")
	}

	privateKey, publicKey, err := loadOrGenerateJWTKeys(cfg, allowEphemeralKeys, logger)
	if err != nil {
		logger.Error("jwt key setup failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	otelShutdown, tracer, err := telemetry.Setup(ctx, "kuruops-api", cfg.OTelExporterOTLPEndpoint)
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

	// Shared across every Settings-mutating service below -- one stateless
	// instance, same pattern as pool itself. See domain.AdminAuditEvent's
	// doc comment for what it records and why.
	adminAuditRepo := repository.NewAdminAuditEventRepository()

	// TagService is a dependency of AlertService/IncidentService (tag
	// catalog validation for UpdateTags), so it's constructed first.
	tagRepo := repository.NewTagRepository()
	tagService := service.NewTagService(pool, tagRepo, adminAuditRepo)
	tagHandlers := handlers.NewTagHandlers(tagService)

	// eventBroadcaster fans out live alert/incident updates over SSE (see
	// internal/events, EventsHandlers.Stream). Publish sends a Postgres
	// NOTIFY and Start's LISTEN connection feeds it back to this process's
	// subscribers -- that's what makes it work across multiple cmd/api
	// replicas AND across processes: cmd/ingest wires its own Broadcaster
	// the same way (see cmd/ingest/main.go), so an alert created there
	// reaches a browser tab connected to any api replica, not just its own
	// process.
	eventBroadcaster := events.NewBroadcaster(pool.Pool, logger)
	safego.Go("api.eventBroadcaster", func() { eventBroadcaster.Start(ctx) })
	eventsHandlers := handlers.NewEventsHandlers(eventBroadcaster)

	alertRepo := repository.NewAlertRepository()
	alertService := service.NewAlertService(pool, alertRepo, tagService, repository.NewPlaybookRepository())
	alertService.EnableEventPublishing(eventBroadcaster.Publish)

	// userService is needed by IncidentHandlers/AlertHandlers (resolving a
	// commenter's display name -- see IncidentHandlers.addComment) as well
	// as its own Settings -> Users & Roles handlers below, so it's
	// constructed here.
	userRepo := repository.NewUserRepository()
	userService := service.NewUserService(pool, userRepo, adminAuditRepo)

	roleRepo := repository.NewRoleRepository()
	roleService := service.NewRoleService(pool, roleRepo, adminAuditRepo)
	roleHandlers := handlers.NewRoleHandlers(roleService)

	incidentSLARepo := repository.NewIncidentSLARepository()
	incidentSLAService := service.NewIncidentSLAService(pool, incidentSLARepo, adminAuditRepo)
	incidentSLAHandlers := handlers.NewIncidentSLAHandlers(incidentSLAService)

	incidentRepo := repository.NewIncidentRepository()
	incidentService := service.NewIncidentService(pool, incidentRepo, tagService, userRepo, incidentSLAService)
	incidentService.EnableEventPublishing(eventBroadcaster.Publish)

	// secrets.Store backend is selected by SECRETS_BACKEND -- "env" (default)
	// is PersistentEnvStore, encrypted and Postgres-backed (see
	// internal/secrets/persistent_store.go). Declared here (moved up from
	// its old spot below) because AIAnalysisService needs it to resolve the
	// tenant's LLM provider API key.
	secretStore, err := secrets.NewFromConfig(ctx, cfg, pool)
	if err != nil {
		logger.Error("secrets backend setup failed", "backend", cfg.SecretsBackend, "error", err)
		os.Exit(1)
	}
	llmProviderRepo := repository.NewLLMProviderRepository()

	// mcpServerRepo/mcpToolService are needed by AIAnalysisService's
	// agentic tool-use loop (ProposeToolCall), so they're constructed here
	// rather than down with the rest of MCP Servers settings wiring below.
	mcpServerRepo := repository.NewMCPServerRepository()
	mcpServerService := service.NewMCPServerService(pool, mcpServerRepo, secretStore, adminAuditRepo)
	aiToolCallRepo := repository.NewAIToolCallRepository()
	mcpToolService := service.NewMCPToolService(pool, mcpServerRepo, aiToolCallRepo, secretStore)
	mcpServerHandlers := handlers.NewMCPServerHandlers(mcpServerService, mcpToolService)

	auditExportService := service.NewAuditExportService(pool, repository.NewAuditRepository())
	auditExportHandlers := handlers.NewAuditExportHandlers(auditExportService)

	adminAuditLogService := service.NewAdminAuditLogService(pool, adminAuditRepo, userService)
	adminAuditLogHandlers := handlers.NewAdminAuditLogHandlers(adminAuditLogService)

	dbMigrationService := dbmigrate.NewService(cfg.MigrationsPath)
	dbMigrationHandlers := handlers.NewDatabaseMigrationHandlers(dbMigrationService, pool)

	aiAnalysisRunRepo := repository.NewAIAnalysisRunRepository()
	aiAnalysisService := service.NewAIAnalysisService(pool, llmProviderRepo, alertRepo, incidentRepo, secretStore, mcpServerRepo, mcpToolService, aiAnalysisRunRepo, aiToolCallRepo)
	mcpToolService.SetOnToolCallResolved(aiAnalysisService.ResumeAnalysisRun)
	// StartAlertAnalysis/StartIncidentAnalysis run the actual LLM call in
	// their own goroutine (see AIAnalysisService's doc comment) -- this is
	// what tells a connected AlertDetailPage/IncidentDetailPage to reload
	// once it's done, the same live-update mechanism every other change
	// already uses.
	aiAnalysisService.EnableEventPublishing(eventBroadcaster.Publish)
	// So GET /alerts/{id} and GET /incidents/{id} surface the latest
	// analysis (completed, running, or failed -- see LatestAnalysisStatus)
	// automatically. The auto-trigger itself lives in cmd/ingest, not here;
	// cmd/api only needs to be able to *show* the result.
	alertService.EnableAnalysisLookup(aiAnalysisRunRepo)
	incidentService.EnableAnalysisLookup(aiAnalysisRunRepo)

	postmortemService := service.NewPostmortemService(incidentService, aiAnalysisService)
	incidentReportService := service.NewIncidentReportService(incidentService)
	incidentHandlers := handlers.NewIncidentHandlers(incidentService, userService, aiAnalysisService, postmortemService, incidentReportService, mcpToolService)

	playbookRepo := repository.NewPlaybookRepository()
	playbookService := service.NewPlaybookService(pool, playbookRepo, alertRepo, cfg.AppBaseURL)
	playbookHandlers := handlers.NewPlaybookHandlers(playbookService)

	dashboardRepo := repository.NewDashboardRepository()
	dashboardService := service.NewDashboardService(pool, dashboardRepo, alertService, incidentService)
	dashboardHandlers := handlers.NewDashboardHandlers(dashboardService)

	webhookRepo := repository.NewWebhookRepository()
	webhookService := service.NewWebhookService(pool, webhookRepo, adminAuditRepo)
	webhookHandlers := handlers.NewWebhookHandlers(webhookService)

	fieldMappingTemplateService := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository())
	fieldMappingTemplateHandlers := handlers.NewFieldMappingTemplateHandlers(fieldMappingTemplateService)

	llmProviderService := service.NewLLMProviderService(pool, llmProviderRepo, secretStore, adminAuditRepo)
	llmProviderHandlers := handlers.NewLLMProviderHandlers(llmProviderService)

	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		logger.Error("upload dir setup failed", "dir", cfg.UploadDir, "error", err)
		os.Exit(1)
	}
	oauthStateService := service.NewOAuthStateService(pool, repository.NewOAuthStateRepository())

	storageConfigRepo := repository.NewStorageConfigRepository()
	storageConfigService := service.NewStorageConfigService(pool, storageConfigRepo, secretStore, cfg.UploadDir,
		oauthStateService, cfg.GoogleOAuthClientID, cfg.GoogleOAuthClientSecret, cfg.AppBaseURL+"/auth/oauth/gdrive/callback", adminAuditRepo)
	storageConfigHandlers := handlers.NewStorageConfigHandlers(storageConfigService)
	uploadKeyRepo := repository.NewUploadKeyRepository()
	uploadKeyService := service.NewUploadKeyService(pool, uploadKeyRepo)
	uploadHandlers := handlers.NewUploadHandlers(storageConfigService, alertService, incidentService, uploadKeyService)

	smtpConfigRepo := repository.NewSMTPConfigRepository()
	smtpConfigService := service.NewSMTPConfigService(pool, smtpConfigRepo, secretStore, mailer.SMTPSender{}, adminAuditRepo)
	smtpConfigHandlers := handlers.NewSMTPConfigHandlers(smtpConfigService)

	slackConfigService := service.NewSlackConfigService(pool, repository.NewSlackConfigRepository(), secretStore,
		oauthStateService, cfg.SlackClientID, cfg.SlackClientSecret, cfg.AppBaseURL+"/auth/oauth/slack/callback", adminAuditRepo)
	slackConfigHandlers := handlers.NewSlackConfigHandlers(slackConfigService)

	retentionConfigService := service.NewRetentionConfigService(pool, repository.NewRetentionConfigRepository(), adminAuditRepo)
	retentionConfigHandlers := handlers.NewRetentionConfigHandlers(retentionConfigService)

	tenantRepo := repository.NewTenantRepository()
	onCallScheduleRepo := repository.NewOnCallScheduleRepository()
	onCallShiftService := service.NewOnCallScheduleService(pool, onCallScheduleRepo, userRepo, tenantRepo, adminAuditRepo)
	onCallShiftHandlers := handlers.NewOnCallScheduleHandlers(onCallShiftService)

	escalationPolicyService := service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), onCallScheduleRepo, onCallShiftService, userService, secretStore, adminAuditRepo)
	escalationPolicyHandlers := handlers.NewEscalationPolicyHandlers(escalationPolicyService)

	// Escalate (POST /alerts/{id}/escalate) needs IncidentService to
	// create+link the promoted incident and EscalationPolicyService for its
	// manual-escalation side effect (see AlertService.Escalate) -- wired
	// here, after both are constructed, rather than as constructor
	// parameters (see EnableEscalation's doc comment).
	alertService.EnableEscalation(incidentService, escalationPolicyService, cfg.AppBaseURL)
	alertHandlers := handlers.NewAlertHandlers(alertService, aiAnalysisService, mcpToolService, userService)

	issuer := authn.NewIssuer(privateKey)
	verifier := authn.NewVerifier(publicKey)
	refreshTokenRepo := repository.NewRefreshTokenRepository()
	authService := service.NewAuthService(pool, tenantRepo, userRepo, refreshTokenRepo, repository.NewMFAPendingTokenRepository(), roleService, issuer, secretStore)
	userHandlers := handlers.NewUserHandlers(userService, authService)

	identityCfgRepo := repository.NewIdentityConfigRepository()
	identityCfgService := service.NewIdentityConfigService(pool, identityCfgRepo, secretStore, adminAuditRepo)
	identityCfgHandlers := handlers.NewIdentityConfigHandlers(identityCfgService)

	ldapAuthService := service.NewLDAPAuthService(pool, identityCfgRepo, secretStore, authService)
	samlAuthService := service.NewSAMLAuthService(pool, identityCfgRepo, secretStore, authService)
	identityCfgService.SetOnSAMLConfigChanged(samlAuthService.InvalidateMetadataCache)
	passwordResetRepo := repository.NewPasswordResetRepository()
	passwordResetService := service.NewPasswordResetService(pool, passwordResetRepo, userRepo, refreshTokenRepo, smtpConfigService, cfg.AppBaseURL)
	authHandlers := handlers.NewAuthHandlers(ctx, pool.Pool, authService, ldapAuthService, samlAuthService, passwordResetService)
	apiTokenService := service.NewUserAPITokenService(pool, repository.NewUserAPITokenRepository(), userRepo)
	accountHandlers := handlers.NewAccountHandlers(authService, apiTokenService)

	// Every 3rd-party OAuth provider's callback (Google Drive, Slack) --
	// needs authService to resolve the tenant the same way AuthHandlers'
	// SAML ACS endpoint does, so this is wired here, after authService
	// exists, even though storageConfigService/slackConfigService
	// (constructed earlier) are its other dependencies -- see
	// OAuthCallbackHandlers' doc comment.
	oauthCallbackHandlers := handlers.NewOAuthCallbackHandlers(authService, storageConfigService, slackConfigService, cfg.AppBaseURL)

	authMiddleware := middleware.JWTAuth(verifier, apiTokenService)
	if useDevHeaderAuth {
		authMiddleware = middleware.DevHeaderAuth
	}

	// Login endpoints are unauthenticated by definition -- rate limited to
	// prevent brute force attacks. Built here (not inside httpserver.NewRouter)
	// because it needs pool -- see middleware.NewRateLimiter.
	loginRateLimiter := middleware.NewRateLimiter(ctx, pool.Pool, "login_ip", cfg.LoginRateLimitPerMinute, time.Minute)

	router := httpserver.NewRouter(httpserver.Options{
		AlertHandlers:                alertHandlers,
		IncidentHandlers:             incidentHandlers,
		PlaybookHandlers:             playbookHandlers,
		DashboardHandlers:            dashboardHandlers,
		TagHandlers:                  tagHandlers,
		WebhookHandlers:              webhookHandlers,
		FieldMappingTemplateHandlers: fieldMappingTemplateHandlers,
		LLMProviderHandlers:          llmProviderHandlers,
		MCPServerHandlers:            mcpServerHandlers,
		UserHandlers:                 userHandlers,
		RoleHandlers:                 roleHandlers,
		AuthHandlers:                 authHandlers,
		LoginRateLimiter:             loginRateLimiter,
		AccountHandlers:              accountHandlers,
		IdentityConfigHandlers:       identityCfgHandlers,
		UploadHandlers:               uploadHandlers,
		StorageConfigHandlers:        storageConfigHandlers,
		SMTPConfigHandlers:           smtpConfigHandlers,
		SlackConfigHandlers:          slackConfigHandlers,
		OnCallScheduleHandlers:       onCallShiftHandlers,
		IncidentSLAHandlers:          incidentSLAHandlers,
		EscalationPolicyHandlers:     escalationPolicyHandlers,
		AuditExportHandlers:          auditExportHandlers,
		AdminAuditLogHandlers:        adminAuditLogHandlers,
		RetentionConfigHandlers:      retentionConfigHandlers,
		DatabaseMigrationHandlers:    dbMigrationHandlers,
		EventsHandlers:               eventsHandlers,
		OAuthCallbackHandlers:        oauthCallbackHandlers,
		AuthMiddleware:               authMiddleware,
		Logger:                       logger,
		HealthCheck:                  httpserver.HealthCheck(pool.Pool),
		HTTPRequestTimeout:           cfg.HTTPRequestTimeout,
		DatabaseMigrationTimeout:     cfg.DatabaseMigrationTimeout,
		Tracer:                       tracer,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	safego.Go("api.ListenAndServe", func() {
		logger.Info("api listening", "addr", cfg.HTTPAddr)
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

// loadOrGenerateJWTKeys loads the configured RSA keypair, or — only when
// allowEphemeral is set (AUTH_MODE=dev or dev-headers) with no paths
// configured — loads a previously-generated one from cfg.DevKeysDir, or
// generates and persists a new one there if this is the first run. Every
// restart after the first reuses the same keypair (so a token issued before
// a restart still verifies after it), instead of a fresh one invalidating
// every session on every `task deploy:up`. See authn.GenerateEphemeralKeyPair
// for why this generated keypair must never be used outside dev.
func loadOrGenerateJWTKeys(cfg config.Config, allowEphemeral bool, logger *slog.Logger) (*rsa.PrivateKey, *rsa.PublicKey, error) {
	if cfg.JWTPrivateKeyPath == "" && cfg.JWTPublicKeyPath == "" {
		if !allowEphemeral {
			return nil, nil, errors.New("JWT_PRIVATE_KEY_PATH and JWT_PUBLIC_KEY_PATH are required outside AUTH_MODE=dev/dev-headers")
		}

		devPrivatePath := filepath.Join(cfg.DevKeysDir, "jwt_private.pem")
		devPublicPath := filepath.Join(cfg.DevKeysDir, "jwt_public.pem")
		if privateKey, err := authn.LoadPrivateKey(devPrivatePath); err == nil {
			publicKey, err := authn.LoadPublicKey(devPublicPath)
			if err != nil {
				return nil, nil, fmt.Errorf("load persisted dev jwt public key: %w", err)
			}
			logger.Info("loaded persisted dev jwt keypair", "dir", cfg.DevKeysDir)
			return privateKey, publicKey, nil
		}

		logger.Warn("no persisted dev jwt keypair found: generating and saving one (dev mode)", "dir", cfg.DevKeysDir)
		key, err := authn.GenerateEphemeralKeyPair()
		if err != nil {
			return nil, nil, err
		}
		if err := authn.SaveKeyPair(cfg.DevKeysDir, key); err != nil {
			return nil, nil, fmt.Errorf("save dev jwt keypair: %w", err)
		}
		return key, &key.PublicKey, nil
	}

	privateKey, err := authn.LoadPrivateKey(cfg.JWTPrivateKeyPath)
	if err != nil {
		return nil, nil, err
	}
	publicKey, err := authn.LoadPublicKey(cfg.JWTPublicKeyPath)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, publicKey, nil
}
