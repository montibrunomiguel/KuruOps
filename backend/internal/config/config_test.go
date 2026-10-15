package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/config"
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
	t.Setenv("DATABASE_URL", "postgres://localhost/kuruops")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "postgres://localhost/kuruops", cfg.DatabaseURL)
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
	t.Setenv("DATABASE_URL", "postgres://localhost/kuruops")
	t.Setenv("SECRETS_BACKEND", "vault")
	t.Setenv("VAULT_ADDR", "https://vault.internal:8200")
	t.Setenv("VAULT_TOKEN", "s.abc123")
	t.Setenv("VAULT_MOUNT", "kuruops-secrets")
	t.Setenv("KMS_REGION", "us-east-1")
	t.Setenv("KMS_ACCESS_KEY_ID", "AKIA...")
	t.Setenv("KMS_SECRET_ACCESS_KEY", "shh")
	t.Setenv("KMS_KEY_ID", "arn:aws:kms:us-east-1:000000000000:key/fake")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "vault", cfg.SecretsBackend)
	assert.Equal(t, "https://vault.internal:8200", cfg.VaultAddr)
	assert.Equal(t, "s.abc123", cfg.VaultToken)
	assert.Equal(t, "kuruops-secrets", cfg.VaultMount)
	assert.Equal(t, "us-east-1", cfg.KMSRegion)
	assert.Equal(t, "AKIA...", cfg.KMSAccessKeyID)
	assert.Equal(t, "shh", cfg.KMSSecretAccessKey)
	assert.Equal(t, "arn:aws:kms:us-east-1:000000000000:key/fake", cfg.KMSKeyID)
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/kuruops")
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
	t.Setenv("DATABASE_URL", "postgres://localhost/kuruops")
	t.Setenv("SHUTDOWN_TIMEOUT", "not-a-duration")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, 15*time.Second, cfg.ShutdownTimeout, "an unparsable duration must fall back, not propagate as an error")
}

func TestLoad_InvalidInt32FallsBackToDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/kuruops")
	t.Setenv("LOGIN_RATE_LIMIT_PER_MINUTE", "not-a-number")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, 20, cfg.LoginRateLimitPerMinute, "an unparsable int must fall back, not propagate as an error")
}

func TestLoad_RateLimitOverridesFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/kuruops")
	t.Setenv("LOGIN_RATE_LIMIT_PER_MINUTE", "5")
	t.Setenv("WEBHOOK_RATE_LIMIT_PER_MINUTE", "120")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, 5, cfg.LoginRateLimitPerMinute)
	assert.Equal(t, 120, cfg.WebhookRateLimitPerMinute)
}

func TestLoad_HTTPServerTimeoutDefaultsAndOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/kuruops")
	for _, k := range []string{"HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT"} {
		t.Setenv(k, "")
	}

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, 2*time.Minute, cfg.HTTPReadTimeout)
	assert.Equal(t, 90*time.Second, cfg.HTTPWriteTimeout)
	assert.Equal(t, 120*time.Second, cfg.HTTPIdleTimeout)
	assert.Greater(t, cfg.HTTPWriteTimeout, 30*time.Second,
		"the write timeout must outlive the default 30s request timeout, or the 504 it writes is cut off")

	t.Setenv("HTTP_WRITE_TIMEOUT", "45s")
	cfg, err = config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, 45*time.Second, cfg.HTTPWriteTimeout)
}

func TestConfigValidate(t *testing.T) {
	good := "postgres://kuruops_app:s3cure-Rand0m-pw@db:5432/kuruops"
	weak := "postgres://kuruops_app:kuruops_app@db:5432/kuruops"

	tests := []struct {
		name    string
		cfg     config.Config
		wantErr string
	}{
		{"jwt with a strong password", config.Config{AuthMode: "jwt", DatabaseURL: good}, ""},
		{"jwt refuses the shipped app password", config.Config{AuthMode: "jwt", DatabaseURL: weak}, "well-known database password"},
		{"jwt refuses the shipped worker password", config.Config{AuthMode: "jwt", DatabaseURL: "postgres://kuruops_worker:kuruops_worker@db/kuruops"}, "well-known database password"},
		{"jwt refuses postgres/postgres", config.Config{AuthMode: "jwt", DatabaseURL: "postgres://postgres:postgres@db/kuruops"}, "well-known database password"},
		{"the comparison ignores case", config.Config{AuthMode: "jwt", DatabaseURL: "postgres://u:ChangeMe@db/kuruops"}, "well-known database password"},
		{"jwt refuses an empty password", config.Config{AuthMode: "jwt", DatabaseURL: "postgres://kuruops_app@db/kuruops"}, "(empty)"},
		{"jwt accepts a weak password when explicitly allowed", config.Config{AuthMode: "jwt", DatabaseURL: weak, AllowDefaultCredentials: true}, ""},
		{"dev mode is exempt -- docker-compose defaults are for it", config.Config{AuthMode: "dev", DatabaseURL: weak}, ""},
		{"dev-headers needs the explicit acknowledgement", config.Config{AuthMode: "dev-headers", DatabaseURL: weak}, "ALLOW_INSECURE_DEV_AUTH=true"},
		{"dev-headers starts once acknowledged", config.Config{AuthMode: "dev-headers", DatabaseURL: weak, AllowInsecureDevAuth: true}, ""},
		{"the acknowledgement alone does not enable anything else", config.Config{AuthMode: "jwt", DatabaseURL: weak, AllowInsecureDevAuth: true}, "well-known database password"},
		{"a typo'd mode is an error, not a silent jwt", config.Config{AuthMode: "dev_headers", DatabaseURL: good}, "unknown AUTH_MODE"},
		{"an unparseable url is left for the pool to report", config.Config{AuthMode: "jwt", DatabaseURL: "://not a url"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}

	t.Run("the error never echoes a non-default password", func(t *testing.T) {
		// Only well-known passwords are quoted back; a strong one that happens
		// to share a URL with a weak username must not end up in a log line.
		err := config.Config{AuthMode: "dev-headers", DatabaseURL: good}.Validate()
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "s3cure-Rand0m-pw")
	})
}

func TestLoad_InsecureOverridesAreExactlyTrue(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/kuruops")
	for _, v := range []string{"1", "yes", "TRUE", "True"} {
		t.Setenv("ALLOW_INSECURE_DEV_AUTH", v)
		t.Setenv("ALLOW_DEFAULT_CREDENTIALS", v)
		cfg, err := config.Load(nil)
		require.NoError(t, err)
		assert.False(t, cfg.AllowInsecureDevAuth, "%q must not count as opting in to an insecure mode", v)
		assert.False(t, cfg.AllowDefaultCredentials, "%q must not count as opting in", v)
	}
	t.Setenv("ALLOW_INSECURE_DEV_AUTH", "true")
	t.Setenv("ALLOW_DEFAULT_CREDENTIALS", "true")
	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.True(t, cfg.AllowInsecureDevAuth)
	assert.True(t, cfg.AllowDefaultCredentials)
}
