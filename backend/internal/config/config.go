// Package config loads process configuration from environment variables.
// Every cmd/* binary (api, ingest, worker) shares this loader so the three
// services stay configurable the same way in Kubernetes (env from Secret/ConfigMap).
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// DatabaseURL must point to a role WITHOUT bypassrls / table ownership
	// (e.g. kuruops_app), otherwise row-level security has no effect.
	DatabaseURL string

	HTTPAddr string

	// JWTPublicKeyPath verifies tokens issued by the identity broker
	// (local login, LDAP, or SAML all converge on the same JWT after login).
	JWTPublicKeyPath string

	// JWTPrivateKeyPath signs session tokens. Only cmd/api needs this (it's
	// the only service with login endpoints); cmd/ingest and cmd/worker
	// don't set it.
	JWTPrivateKeyPath string

	// AuthMode "dev" enables the trust-any-header auth bypass
	// (middleware.DevHeaderAuth). Anything else requires real JWT
	// verification. Never set to "dev" outside a developer's own machine.
	AuthMode string

	// DevKeysDir is where cmd/api persists the JWT keypair it generates for
	// AUTH_MODE=dev/dev-headers when JWT_PRIVATE_KEY_PATH/JWT_PUBLIC_KEY_PATH
	// aren't set -- see main.loadOrGenerateJWTKeys. Only read in that
	// ephemeral-keys path; jwt mode always requires explicit key paths.
	DevKeysDir string

	ShutdownTimeout time.Duration

	// UploadDir is where uploaded comment/close-classification images are
	// written to and served from (see handlers.UploadHandlers). Only cmd/api
	// needs this -- ingest/worker never handle uploads.
	UploadDir string

	// AppBaseURL is the externally-reachable origin of the frontend (e.g.
	// https://kuruops.example.com), used to build links embedded in
	// outbound email (password reset). Only cmd/api needs this.
	AppBaseURL string

	// SecretsBackend selects the secrets.Store implementation cmd/api (and
	// cmd/ingest/worker, wherever they resolve a secret) construct at
	// startup -- "env" (default) is secrets.PersistentEnvStore (encrypted,
	// Postgres-backed), "vault" and "kms" are real external backends. See
	// secrets.NewFromConfig for the factory switch this drives.
	SecretsBackend string

	// SecretsEncryptionKey is the base64 of 32 random bytes (AES-256),
	// required when SecretsBackend is "" or "env" -- see
	// secrets.NewPersistentEnvStore. Generate with `openssl rand -base64 32`.
	SecretsEncryptionKey string

	// Vault* configure secrets.VaultStore, only read when SecretsBackend="vault".
	VaultAddr  string
	VaultToken string
	// VaultMount is the KV v2 secrets engine's mount path (data lives under
	// <mount>/data/<path>), not the path to an individual secret.
	VaultMount string

	// KMS* configure secrets.AWSKMSStore, only read when SecretsBackend="kms".
	// Static credentials, not an ambient IAM role -- same reasoning as
	// blobstore.NewS3Store: KuruOps is self-hosted and may not be running
	// inside AWS at all.
	KMSRegion          string
	KMSAccessKeyID     string
	KMSSecretAccessKey string
	KMSKeyID           string

	// MigrationsPath is where db/migrations' .up.sql/.down.sql files live on
	// disk -- baked into the api image at this exact path (see
	// backend/Dockerfile). Only cmd/api needs this (internal/dbmigrate, the
	// external-database-migration Settings feature).
	MigrationsPath string

	// DBPoolMaxConns/DBPoolMinConns override pgxpool's own defaults (see
	// db.NewPool) -- 0 (unset) leaves pgxpool's default in effect (the
	// greater of 4 or runtime.NumCPU() for MaxConns). Worth setting
	// explicitly once running more than one replica of a binary: pgxpool's
	// per-process default was sized for a single instance talking to
	// Postgres, and N replicas each defaulting independently can add up to
	// more total connections than Postgres' own max_connections allows.
	// Recommended starting point: Postgres max_connections divided by the
	// number of replicas of this binary, leaving headroom for the other two
	// binaries and any direct/admin connections.
	DBPoolMaxConns int32
	DBPoolMinConns int32

	// HTTPRequestTimeout bounds how long any single /api/v1 request (other
	// than the long-lived SSE stream, /api/v1/events/stream -- see
	// router.go's doc comment on why that one is exempt) can run before the
	// request's context is canceled. Without this, a saturated connection
	// pool (see db.NewPool's DBPoolMaxConns doc comment) leaves a caller
	// blocked on pool.Acquire indefinitely instead of failing cleanly --
	// pgx's Acquire/Query/Exec all respect the request context's deadline,
	// so this is what actually turns "hangs forever" into "fails after N
	// seconds so the client (and whatever's watching kuruops_http_requests_
	// 5xx_total on /metrics) finds out something is wrong."
	HTTPRequestTimeout time.Duration

	// DatabaseMigrationTimeout bounds Settings -> External Database's
	// /migrate request -- deliberately much larger than HTTPRequestTimeout
	// and applied via its own chimw.Timeout in router.go (that route is
	// registered outside the HTTPRequestTimeout-bound group, same as
	// /events/stream) since a real copy can take minutes, not seconds, but
	// still shouldn't be allowed to hold a connection open forever if the
	// target genuinely hangs.
	DatabaseMigrationTimeout time.Duration

	// LoginRateLimitPerMinute/WebhookRateLimitPerMinute configure the
	// per-account login limiter (cmd/api) and per-IP webhook limiter
	// (cmd/ingest) -- see middleware.NewRateLimiter's callers in each
	// main.go. Previously hardcoded; every other operational tunable in
	// this struct is env-var driven, so these were the odd ones out.
	LoginRateLimitPerMinute   int
	WebhookRateLimitPerMinute int

	// OTelExporterOTLPEndpoint, when set, points telemetry.Setup at an
	// OTLP/HTTP collector (e.g. "http://otel-collector:4318") and enables
	// real distributed tracing. Empty (the default) keeps tracing fully
	// inert -- see telemetry.Setup's doc comment.
	OTelExporterOTLPEndpoint string

	// GoogleOAuthClientID/Secret are the app-level Google OAuth client
	// every tenant's Settings -> Storage Integration "Connect your Google
	// account" flow authenticates through (see
	// StorageConfigService.GetGDriveAuthorizeURL). Optional -- empty
	// disables that path entirely; the service-account alternative is
	// unaffected. The redirect URI is derived from AppBaseURL, not a
	// separate env var (it must always match AppBaseURL's origin anyway).
	GoogleOAuthClientID     string
	GoogleOAuthClientSecret string

	// SlackClientID/Secret are the Slack App's OAuth credentials every
	// tenant's Settings -> Conectores -> Slack "Conectar ao Slack" flow
	// authenticates through (see SlackConfigService.GetAuthorizeURL).
	// Optional -- empty disables that path entirely, same convention as
	// GoogleOAuthClientID/Secret. SlackSigningSecret verifies inbound
	// requests from Slack (Events API, slash commands) -- unused by this
	// foundation phase (no inbound receiver yet) but the Slack App fixes
	// this value at creation time regardless, so it's read now to avoid
	// asking the admin to re-paste it into a later PR.
	SlackClientID      string
	SlackClientSecret  string
	SlackSigningSecret string
}

func Load(logger *slog.Logger) (Config, error) {
	cfg := Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		HTTPAddr:          getEnvDefault("HTTP_ADDR", ":8080"),
		JWTPublicKeyPath:  os.Getenv("JWT_PUBLIC_KEY_PATH"),
		JWTPrivateKeyPath: os.Getenv("JWT_PRIVATE_KEY_PATH"),
		AuthMode:          getEnvDefault("AUTH_MODE", "jwt"),
		DevKeysDir:        getEnvDefault("DEV_KEYS_DIR", ".dev-keys"),
		ShutdownTimeout:   getEnvDurationDefault(logger, "SHUTDOWN_TIMEOUT", 15*time.Second),
		UploadDir:         getEnvDefault("UPLOAD_DIR", "/data/uploads"),
		AppBaseURL:        getEnvDefault("APP_BASE_URL", "http://localhost:3000"),

		SecretsBackend:       getEnvDefault("SECRETS_BACKEND", "env"),
		SecretsEncryptionKey: os.Getenv("SECRETS_ENCRYPTION_KEY"),
		VaultAddr:            os.Getenv("VAULT_ADDR"),
		VaultToken:           os.Getenv("VAULT_TOKEN"),
		VaultMount:           getEnvDefault("VAULT_MOUNT", "secret"),

		KMSRegion:          os.Getenv("KMS_REGION"),
		KMSAccessKeyID:     os.Getenv("KMS_ACCESS_KEY_ID"),
		KMSSecretAccessKey: os.Getenv("KMS_SECRET_ACCESS_KEY"),
		KMSKeyID:           os.Getenv("KMS_KEY_ID"),

		MigrationsPath: getEnvDefault("MIGRATIONS_PATH", "/app/db/migrations"),

		DBPoolMaxConns: getEnvInt32Default(logger, "DB_POOL_MAX_CONNS", 0),
		DBPoolMinConns: getEnvInt32Default(logger, "DB_POOL_MIN_CONNS", 0),

		HTTPRequestTimeout:        getEnvDurationDefault(logger, "HTTP_REQUEST_TIMEOUT", 30*time.Second),
		DatabaseMigrationTimeout:  getEnvDurationDefault(logger, "DATABASE_MIGRATION_TIMEOUT", 10*time.Minute),
		LoginRateLimitPerMinute:   int(getEnvInt32Default(logger, "LOGIN_RATE_LIMIT_PER_MINUTE", 20)),
		WebhookRateLimitPerMinute: int(getEnvInt32Default(logger, "WEBHOOK_RATE_LIMIT_PER_MINUTE", 60)),

		OTelExporterOTLPEndpoint: getEnvDefault("OTEL_EXPORTER_OTLP_ENDPOINT", ""),

		GoogleOAuthClientID:     os.Getenv("GOOGLE_OAUTH_CLIENT_ID"),
		GoogleOAuthClientSecret: os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET"),

		SlackClientID:      os.Getenv("SLACK_CLIENT_ID"),
		SlackClientSecret:  os.Getenv("SLACK_CLIENT_SECRET"),
		SlackSigningSecret: os.Getenv("SLACK_SIGNING_SECRET"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	return cfg, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getEnvDurationDefault/getEnvInt32Default warn (rather than silently
// falling back) when the env var is SET but fails to parse -- a bare
// "unset" is expected and stays quiet, but a typo'd value (e.g.
// HTTP_REQUEST_TIMEOUT=3oh) reverting to the default with zero signal is
// exactly the kind of misconfiguration this codebase otherwise prefers to
// fail loudly on (see SECRETS_ENCRYPTION_KEY). logger may be nil (e.g. in
// tests that don't care about the warning) -- callers that pass nil just
// don't get the log line, parsing behavior is unaffected either way.
func getEnvDurationDefault(logger *slog.Logger, key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		if logger != nil {
			logger.Warn("env var set but unparseable, using default", "key", key, "value", v, "default", def, "error", err)
		}
		return def
	}
	return d
}

func getEnvInt32Default(logger *slog.Logger, key string, def int32) int32 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		if logger != nil {
			logger.Warn("env var set but unparseable, using default", "key", key, "value", v, "default", def, "error", err)
		}
		return def
	}
	return int32(n)
}
