// Package config loads process configuration from environment variables.
// Every cmd/* binary (api, ingest, worker) shares this loader so the three
// services stay configurable the same way in Kubernetes (env from Secret/ConfigMap).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
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

	// AuthMode is "jwt" (the default: real JWT verification, explicit keys),
	// "dev" (real auth, but a throwaway/persisted keypair is generated when no
	// key paths are set) or "dev-headers" (swaps JWT verification for
	// middleware.DevHeaderAuth, which trusts X-Tenant-ID/X-User-ID/X-Is-Admin
	// verbatim -- a complete authentication bypass). Validate refuses
	// "dev-headers" unless AllowInsecureDevAuth is also set, and refuses an
	// unknown value. Never use "dev" or "dev-headers" outside a developer's
	// own machine.
	AuthMode string

	// AllowInsecureDevAuth (ALLOW_INSECURE_DEV_AUTH=true) is the explicit,
	// separate acknowledgement AUTH_MODE=dev-headers requires. A bare warning
	// log was the only thing standing between that mode and an
	// unauthenticated API when the variable was set by accident (a copied
	// .env, a template left in a manifest); this makes it two deliberate
	// settings instead of one.
	AllowInsecureDevAuth bool

	// AllowDefaultCredentials (ALLOW_DEFAULT_CREDENTIALS=true) lets a
	// production-like process (AUTH_MODE=jwt) start with a well-known
	// database password. Off by default -- see Validate.
	AllowDefaultCredentials bool

	// HTTPReadTimeout/HTTPWriteTimeout/HTTPIdleTimeout are cmd/api's
	// http.Server timeouts. Only ReadHeaderTimeout was set before, so a client
	// that opened a connection and trickled a body, or simply never read the
	// response, held a connection and its goroutine indefinitely.
	//
	// The write timeout is deliberately longer than HTTPRequestTimeout: that
	// bound cancels the handler's context, this one cuts the socket, and the
	// socket must outlive the 504 that chimw.Timeout still has to write. The
	// two routes that legitimately run longer -- the SSE stream and the
	// external-database migration -- manage their own write deadline per
	// request (see handlers.EventsHandlers.Stream and
	// middleware.ExtendWriteDeadline) instead of this being raised for
	// everything.
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration

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
	// UploadBackend is where attachments go for a tenant that has not configured S3,
	// GCS or Google Drive (UPLOAD_BACKEND): "local" (default) writes under UploadDir,
	// "database" keeps them in Postgres (table attachment_blobs). Local disk is
	// per-pod state, which pins the api to one replica; "database" makes the api
	// stateless so it can scale, at the cost of putting attachment bytes in the
	// database -- see migration 0016. Reads fall back to UploadDir, so switching does
	// not strand what was uploaded before.
	UploadBackend string

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
	// ExternalCallBreakerThreshold / ExternalCallBreakerCooldown tune the
	// circuit breaker wrapping every outbound LLM and MCP call (see package
	// circuitbreaker). Threshold is how many consecutive provider faults
	// (transport errors, 5xx, 429 -- never a 4xx that would fail the same
	// way forever) open the breaker for that host; Cooldown is how long it
	// stays open before one probe is let through.
	//
	// Setting either to zero disables breaking entirely and restores the
	// pre-breaker behaviour of dialling a dead provider on every call.
	ExternalCallBreakerThreshold int
	ExternalCallBreakerCooldown  time.Duration

	// LLMCallMaxAttempts / LLMCallRetryBaseDelay tune the in-place retry every
	// LLM call gets (see llmclient.RetryPolicy): a provider's 503 or 429
	// usually clears within seconds, so one blip should not end an analysis.
	// Attempts is the total number of tries (1 disables retrying); the first
	// wait is the base delay, doubling from there. A provider that stays down
	// longer is handled by the analysis queue, on a schedule of minutes.
	LLMCallMaxAttempts    int
	LLMCallRetryBaseDelay time.Duration

	// AIAnalysisConcurrency is how many automatic alert analyses one cmd/analyzer
	// process runs at once (AI_ANALYSIS_CONCURRENCY, default 5). A burst of alerts
	// outpaces an LLM call by far; the rest wait in the database queue. It is per
	// replica, so N analyzer replicas run up to N times this -- size it against the
	// provider's rate limit, not just this process.
	AIAnalysisConcurrency int
	// AIAnalysisPollInterval is how often an idle analyzer looks for due jobs
	// (AI_ANALYSIS_POLL_INTERVAL, default 5s). A newly ingested alert wakes it at
	// once through the event channel, so this only bounds how late a job whose backoff
	// just elapsed -- or whose wake-up event was missed -- is noticed.
	AIAnalysisPollInterval time.Duration

	SlackClientID      string
	SlackClientSecret  string
	SlackSigningSecret string
}

// LoadAndValidate is what every cmd/* does before anything else: read the configuration
// from the environment, then refuse to continue if it is insecure by construction (see
// Validate). One function so the four binaries cannot drift apart on what "valid enough to
// start" means, and so that rule can be tested without launching a process.
func LoadAndValidate(logger *slog.Logger) (Config, error) {
	cfg, err := Load(logger)
	if err != nil {
		return Config{}, fmt.Errorf("config load failed: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("refusing to start: insecure configuration: %w", err)
	}
	return cfg, nil
}

func Load(logger *slog.Logger) (Config, error) {
	cfg := Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		HTTPAddr:          getEnvDefault("HTTP_ADDR", ":8080"),
		JWTPublicKeyPath:  os.Getenv("JWT_PUBLIC_KEY_PATH"),
		JWTPrivateKeyPath: os.Getenv("JWT_PRIVATE_KEY_PATH"),
		AuthMode:          getEnvDefault("AUTH_MODE", "jwt"),

		AllowInsecureDevAuth:    os.Getenv("ALLOW_INSECURE_DEV_AUTH") == "true",
		AllowDefaultCredentials: os.Getenv("ALLOW_DEFAULT_CREDENTIALS") == "true",

		// 2m read covers a 25 MiB attachment (the upload cap) from a ~1.7 Mbit/s
		// client; anything slower than that is the trickling client this exists
		// to cut off.
		HTTPReadTimeout:  getEnvDurationDefault(logger, "HTTP_READ_TIMEOUT", 2*time.Minute),
		HTTPWriteTimeout: getEnvDurationDefault(logger, "HTTP_WRITE_TIMEOUT", 90*time.Second),
		HTTPIdleTimeout:  getEnvDurationDefault(logger, "HTTP_IDLE_TIMEOUT", 120*time.Second),

		DevKeysDir:      getEnvDefault("DEV_KEYS_DIR", ".dev-keys"),
		ShutdownTimeout: getEnvDurationDefault(logger, "SHUTDOWN_TIMEOUT", 15*time.Second),
		UploadDir:       getEnvDefault("UPLOAD_DIR", "/data/uploads"),
		UploadBackend:   getEnvDefault("UPLOAD_BACKEND", "local"),
		AppBaseURL:      getEnvDefault("APP_BASE_URL", "http://localhost:3000"),

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

		ExternalCallBreakerThreshold: int(getEnvInt32Default(logger, "EXTERNAL_CALL_BREAKER_THRESHOLD", 5)),
		ExternalCallBreakerCooldown:  getEnvDurationDefault(logger, "EXTERNAL_CALL_BREAKER_COOLDOWN", 30*time.Second),

		LLMCallMaxAttempts:     int(getEnvInt32Default(logger, "LLM_CALL_MAX_ATTEMPTS", 3)),
		LLMCallRetryBaseDelay:  getEnvDurationDefault(logger, "LLM_CALL_RETRY_BASE_DELAY", time.Second),
		AIAnalysisConcurrency:  int(getEnvInt32Default(logger, "AI_ANALYSIS_CONCURRENCY", 5)),
		AIAnalysisPollInterval: getEnvDurationDefault(logger, "AI_ANALYSIS_POLL_INTERVAL", 5*time.Second),

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

// weakDatabasePasswords are the credentials this repository itself ships as
// local-development defaults (docker-compose.yml, the db/init role scripts,
// Taskfile.yml) plus the placeholders people leave behind. A production-like
// process must not start with one of them: the app role's password is the only
// thing between a network-adjacent attacker and every tenant's rows, and the
// defaults are public in the repo.
var weakDatabasePasswords = map[string]struct{}{
	"":               {},
	"postgres":       {},
	"password":       {},
	"changeme":       {},
	"change_me":      {},
	"admin":          {},
	"root":           {},
	"secret":         {},
	"kuruops":        {},
	"kuruops_app":    {},
	"kuruops_worker": {},
}

// Validate refuses configurations that are insecure by construction, so they
// have to be chosen on purpose rather than arrived at by accident. It is the
// startup counterpart of the SECRETS_ENCRYPTION_KEY check in
// secrets.NewFromConfig (same signal: AUTH_MODE tells it whether this is a
// developer's machine) and is called by every cmd/* binary right after Load.
//
//   - An unknown AUTH_MODE is an error: a typo such as "dev_headers" used to
//     fall through to the JWT path silently, hiding the mistake.
//   - AUTH_MODE=dev-headers additionally needs ALLOW_INSECURE_DEV_AUTH=true. It
//     trusts client-supplied identity headers (including X-Is-Admin), so it
//     must never be reachable by one forgotten environment variable.
//   - AUTH_MODE=jwt (the production-like mode) refuses a well-known database
//     password unless ALLOW_DEFAULT_CREDENTIALS=true. The dev modes are exempt:
//     they are what docker-compose's defaults are for.
//
// Every error names the exact variable that overrides it, so a deliberate
// choice is one line, not an investigation.
func (c Config) Validate() error {
	switch c.AuthMode {
	case "jwt", "dev", "dev-headers":
	default:
		return fmt.Errorf("unknown AUTH_MODE %q: must be one of jwt, dev, dev-headers", c.AuthMode)
	}

	switch c.UploadBackend {
	case "", "local", "database": // empty is the default, local
	default:
		return fmt.Errorf("unknown UPLOAD_BACKEND %q: must be local or database", c.UploadBackend)
	}

	if c.AuthMode == "dev-headers" && !c.AllowInsecureDevAuth {
		return errors.New("AUTH_MODE=dev-headers disables authentication entirely (the API would trust X-Tenant-ID, X-User-ID and X-Is-Admin from any caller); " +
			"refusing to start. If this really is a throwaway local instance, also set ALLOW_INSECURE_DEV_AUTH=true")
	}

	if c.AuthMode == "jwt" && !c.AllowDefaultCredentials {
		if pw, weak := databasePasswordIsWeak(c.DatabaseURL); weak {
			shown := "(empty)"
			if pw != "" {
				shown = fmt.Sprintf("%q", pw)
			}
			return fmt.Errorf("DATABASE_URL uses a well-known database password %s, and AUTH_MODE=jwt marks this as a production-like deployment; "+
				"refusing to start. Set a strong password for the role (see db/init/*.sql), or, for a local setup you accept the risk of, "+
				"set ALLOW_DEFAULT_CREDENTIALS=true or use AUTH_MODE=dev", shown)
		}
	}

	return nil
}

// databasePasswordIsWeak extracts the password from a postgres URL and reports
// whether it is on the weak list. A URL that does not parse reports not-weak:
// db.NewPool fails on it moments later with a better error than this could give.
func databasePasswordIsWeak(databaseURL string) (password string, weak bool) {
	u, err := url.Parse(databaseURL)
	if err != nil || u.User == nil {
		return "", false
	}
	pw, _ := u.User.Password()
	_, weak = weakDatabasePasswords[strings.ToLower(pw)]
	return pw, weak
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
