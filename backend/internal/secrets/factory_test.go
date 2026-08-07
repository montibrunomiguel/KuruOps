package secrets_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/config"
	"github.com/argusops/argusops/internal/secrets"
)

func TestNewFromConfig_DefaultsToEnvStore(t *testing.T) {
	for _, backend := range []string{"", "env"} {
		store, err := secrets.NewFromConfig(config.Config{SecretsBackend: backend})
		require.NoError(t, err)
		assert.IsType(t, &secrets.EnvStore{}, store)
	}
}

func TestNewFromConfig_Vault(t *testing.T) {
	t.Run("requires addr and token", func(t *testing.T) {
		_, err := secrets.NewFromConfig(config.Config{SecretsBackend: "vault"})
		assert.ErrorContains(t, err, "VAULT_ADDR")
	})

	t.Run("builds a VaultStore when configured", func(t *testing.T) {
		store, err := secrets.NewFromConfig(config.Config{
			SecretsBackend: "vault", VaultAddr: "https://vault.internal", VaultToken: "tok", VaultMount: "secret",
		})
		require.NoError(t, err)
		assert.IsType(t, &secrets.VaultStore{}, store)
	})
}

func TestNewFromConfig_KMS(t *testing.T) {
	t.Run("requires all four settings", func(t *testing.T) {
		_, err := secrets.NewFromConfig(config.Config{SecretsBackend: "kms", KMSRegion: "us-east-1"})
		assert.ErrorContains(t, err, "KMS_REGION")
	})

	t.Run("builds an AWSKMSStore when configured", func(t *testing.T) {
		store, err := secrets.NewFromConfig(config.Config{
			SecretsBackend: "kms", KMSRegion: "us-east-1", KMSAccessKeyID: "AKIA...", KMSSecretAccessKey: "secret", KMSKeyID: "key-id",
		})
		require.NoError(t, err)
		assert.IsType(t, &secrets.AWSKMSStore{}, store)
	})
}

func TestNewFromConfig_UnknownBackend(t *testing.T) {
	_, err := secrets.NewFromConfig(config.Config{SecretsBackend: "made-up"})
	assert.ErrorContains(t, err, `unknown SECRETS_BACKEND "made-up"`)
}
