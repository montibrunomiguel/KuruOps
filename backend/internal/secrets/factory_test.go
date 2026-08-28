package secrets_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/config"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/testutil"
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

// exampleSecretsEncryptionKey mirrors the value committed in the repo's
// .env.example -- deliberately duplicated here rather than exported from
// the secrets package, so this test fails loudly if that file's value ever
// drifts from what the guard actually checks against.
const exampleSecretsEncryptionKey = "k83Yh5rQGCdVDMbUtJywr5J6ScE6++Bp4m936YZDecw="

func TestNewFromConfig_RejectsExampleEncryptionKeyOutsideDevMode(t *testing.T) {
	t.Run("the .env.example default key is refused under AUTH_MODE=jwt", func(t *testing.T) {
		_, err := secrets.NewFromConfig(context.Background(), config.Config{
			SecretsBackend: "env", SecretsEncryptionKey: exampleSecretsEncryptionKey, AuthMode: "jwt",
		}, nil)
		assert.ErrorContains(t, err, "still set to the .env.example default")
	})

	// PersistentEnvStore eagerly decrypts every row already in secret_store
	// on construction (see NewPersistentEnvStore), and other tests in this
	// package leave rows behind encrypted under testEncryptionKey with no
	// cleanup -- constructing a store here under a different key
	// (exampleSecretsEncryptionKey) would otherwise fail to decrypt those
	// leftover rows. Clearing the table first isolates these subtests from
	// that shared, un-cleaned-up state; safe because Go runs top-level Test
	// functions in this package sequentially, so no other test is
	// concurrently relying on secret_store's contents at this point.
	pool := testutil.RequireTestDB(t)
	_, err := pool.Exec(context.Background(), `delete from secret_store`)
	require.NoError(t, err)

	t.Run("the .env.example default key is allowed under AUTH_MODE=dev", func(t *testing.T) {
		store, err := secrets.NewFromConfig(context.Background(), config.Config{
			SecretsBackend: "env", SecretsEncryptionKey: exampleSecretsEncryptionKey, AuthMode: "dev",
		}, pool)
		require.NoError(t, err)
		assert.IsType(t, &secrets.PersistentEnvStore{}, store)
	})

	t.Run("the .env.example default key is allowed under AUTH_MODE=dev-headers", func(t *testing.T) {
		store, err := secrets.NewFromConfig(context.Background(), config.Config{
			SecretsBackend: "env", SecretsEncryptionKey: exampleSecretsEncryptionKey, AuthMode: "dev-headers",
		}, pool)
		require.NoError(t, err)
		assert.IsType(t, &secrets.PersistentEnvStore{}, store)
	})

	t.Run("a real key equal in length to the example is still accepted", func(t *testing.T) {
		store, err := secrets.NewFromConfig(context.Background(), config.Config{
			SecretsBackend: "env", SecretsEncryptionKey: testEncryptionKey, AuthMode: "jwt",
		}, pool)
		require.NoError(t, err)
		assert.IsType(t, &secrets.PersistentEnvStore{}, store)
	})
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
