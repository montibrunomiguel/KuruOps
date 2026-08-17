package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/config"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DATABASE_URL", "HTTP_ADDR", "JWT_PUBLIC_KEY_PATH", "JWT_PRIVATE_KEY_PATH",
		"AUTH_MODE", "SHUTDOWN_TIMEOUT", "UPLOAD_DIR",
		"SECRETS_BACKEND", "VAULT_ADDR", "VAULT_TOKEN", "VAULT_MOUNT",
		"KMS_REGION", "KMS_ACCESS_KEY_ID", "KMS_SECRET_ACCESS_KEY", "KMS_KEY_ID",
		"MIGRATIONS_PATH",
	} {
		t.Setenv(key, "")
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	clearEnv(t)
	_, err := config.Load(nil)
	assert.ErrorContains(t, err, "DATABASE_URL is required")
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/argusops")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "postgres://localhost/argusops", cfg.DatabaseURL)
	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, "jwt", cfg.AuthMode)
	assert.Equal(t, 15*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, "/data/uploads", cfg.UploadDir)
	assert.Equal(t, "env", cfg.SecretsBackend)
	assert.Equal(t, "secret", cfg.VaultMount)
	assert.Equal(t, "/app/db/migrations", cfg.MigrationsPath)
	assert.Equal(t, 10*time.Minute, cfg.DatabaseMigrationTimeout)
	assert.Equal(t, 20, cfg.LoginRateLimitPerMinute)
	assert.Equal(t, 60, cfg.WebhookRateLimitPerMinute)
}

func TestLoad_SecretsBackendOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/argusops")
	t.Setenv("SECRETS_BACKEND", "vault")
	t.Setenv("VAULT_ADDR", "https://vault.internal:8200")
	t.Setenv("VAULT_TOKEN", "s.abc123")
	t.Setenv("VAULT_MOUNT", "argusops-secrets")
	t.Setenv("KMS_REGION", "us-east-1")
	t.Setenv("KMS_ACCESS_KEY_ID", "AKIA...")
	t.Setenv("KMS_SECRET_ACCESS_KEY", "shh")
	t.Setenv("KMS_KEY_ID", "arn:aws:kms:us-east-1:000000000000:key/fake")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "vault", cfg.SecretsBackend)
	assert.Equal(t, "https://vault.internal:8200", cfg.VaultAddr)
	assert.Equal(t, "s.abc123", cfg.VaultToken)
	assert.Equal(t, "argusops-secrets", cfg.VaultMount)
	assert.Equal(t, "us-east-1", cfg.KMSRegion)
	assert.Equal(t, "AKIA...", cfg.KMSAccessKeyID)
	assert.Equal(t, "shh", cfg.KMSSecretAccessKey)
	assert.Equal(t, "arn:aws:kms:us-east-1:000000000000:key/fake", cfg.KMSKeyID)
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/argusops")
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("JWT_PUBLIC_KEY_PATH", "/keys/pub.pem")
	t.Setenv("JWT_PRIVATE_KEY_PATH", "/keys/priv.pem")
	t.Setenv("AUTH_MODE", "dev")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, ":9090", cfg.HTTPAddr)
	assert.Equal(t, "/keys/pub.pem", cfg.JWTPublicKeyPath)
	assert.Equal(t, "/keys/priv.pem", cfg.JWTPrivateKeyPath)
	assert.Equal(t, "dev", cfg.AuthMode)
	assert.Equal(t, 30*time.Second, cfg.ShutdownTimeout)
}

func TestLoad_InvalidDurationFallsBackToDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/argusops")
	t.Setenv("SHUTDOWN_TIMEOUT", "not-a-duration")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, 15*time.Second, cfg.ShutdownTimeout, "an unparsable duration must fall back, not propagate as an error")
}

func TestLoad_InvalidInt32FallsBackToDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/argusops")
	t.Setenv("LOGIN_RATE_LIMIT_PER_MINUTE", "not-a-number")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, 20, cfg.LoginRateLimitPerMinute, "an unparsable int must fall back, not propagate as an error")
}

func TestLoad_RateLimitOverridesFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/argusops")
	t.Setenv("LOGIN_RATE_LIMIT_PER_MINUTE", "5")
	t.Setenv("WEBHOOK_RATE_LIMIT_PER_MINUTE", "120")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, 5, cfg.LoginRateLimitPerMinute)
	assert.Equal(t, 120, cfg.WebhookRateLimitPerMinute)
}
