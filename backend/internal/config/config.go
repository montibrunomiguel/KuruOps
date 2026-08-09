// Package config loads process configuration from environment variables.
// Every cmd/* binary (api, ingest, worker) shares this loader so the three
// services stay configurable the same way in Kubernetes (env from Secret/ConfigMap).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// DatabaseURL must point to a role WITHOUT bypassrls / table ownership
	// (e.g. argusops_app), otherwise row-level security has no effect.
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
	// https://argusops.example.com), used to build links embedded in
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
	// blobstore.NewS3Store: ArgusOps is self-hosted and may not be running
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
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		HTTPAddr:          getEnvDefault("HTTP_ADDR", ":8080"),
		JWTPublicKeyPath:  os.Getenv("JWT_PUBLIC_KEY_PATH"),
		JWTPrivateKeyPath: os.Getenv("JWT_PRIVATE_KEY_PATH"),
		AuthMode:          getEnvDefault("AUTH_MODE", "jwt"),
		DevKeysDir:        getEnvDefault("DEV_KEYS_DIR", ".dev-keys"),
		ShutdownTimeout:   getEnvDurationDefault("SHUTDOWN_TIMEOUT", 15*time.Second),
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

		DBPoolMaxConns: getEnvInt32Default("DB_POOL_MAX_CONNS", 0),
		DBPoolMinConns: getEnvInt32Default("DB_POOL_MIN_CONNS", 0),
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

func getEnvDurationDefault(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func getEnvInt32Default(key string, def int32) int32 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return def
	}
	return int32(n)
}
