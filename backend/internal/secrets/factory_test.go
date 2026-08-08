package secrets_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/config"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/testutil"
)

func TestNewFromConfig_DefaultsToPersistentEnvStore(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	for _, backend := range []string{"", "env"} {
		store, err := secrets.NewFromConfig(context.Background(), config.Config{SecretsBackend: backend, SecretsEncryptionKey: testEncryptionKey}, pool)
		require.NoError(t, err)
		assert.IsType(t, &secrets.PersistentEnvStore{}, store)
	}
}

func TestNewFromConfig_RequiresEncryptionKey(t *testing.T) {
	_, err := secrets.NewFromConfig(context.Background(), config.Config{SecretsBackend: "env"}, nil)
	assert.ErrorContains(t, err, "SECRETS_ENCRYPTION_KEY")
}

func TestNewFromConfig_Vault(t *testing.T) {
	t.Run("requires addr and token", func(t *testing.T) {
		_, err := secrets.NewFromConfig(context.Background(), config.Config{SecretsBackend: "vault"}, nil)
		assert.ErrorContains(t, err, "VAULT_ADDR")
	})

	t.Run("builds a VaultStore when configured", func(t *testing.T) {
		store, err := secrets.NewFromConfig(context.Background(), config.Config{
			SecretsBackend: "vault", VaultAddr: "https://vault.internal", VaultToken: "tok", VaultMount: "secret",
		}, nil)
		require.NoError(t, err)
		assert.IsType(t, &secrets.VaultStore{}, store)
	})
}

func TestNewFromConfig_KMS(t *testing.T) {
	t.Run("requires all four settings", func(t *testing.T) {
		_, err := secrets.NewFromConfig(context.Background(), config.Config{SecretsBackend: "kms", KMSRegion: "us-east-1"}, nil)
		assert.ErrorContains(t, err, "KMS_REGION")
	})

	t.Run("builds an AWSKMSStore when configured", func(t *testing.T) {
		store, err := secrets.NewFromConfig(context.Background(), config.Config{
			SecretsBackend: "kms", KMSRegion: "us-east-1", KMSAccessKeyID: "AKIA...", KMSSecretAccessKey: "secret", KMSKeyID: "key-id",
		}, nil)
		require.NoError(t, err)
		assert.IsType(t, &secrets.AWSKMSStore{}, store)
	})
}

func TestNewFromConfig_UnknownBackend(t *testing.T) {
	_, err := secrets.NewFromConfig(context.Background(), config.Config{SecretsBackend: "made-up"}, nil)
	assert.ErrorContains(t, err, `unknown SECRETS_BACKEND "made-up"`)
}
