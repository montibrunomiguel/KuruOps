package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func TestLoadOrGenerateJWTKeys(t *testing.T) {
	t.Run("explicit key paths are loaded directly, ignoring DevKeysDir", func(t *testing.T) {
		dir := t.TempDir()
		key, err := authn.GenerateEphemeralKeyPair()
		require.NoError(t, err)
		require.NoError(t, authn.SaveKeyPair(dir, key))

		cfg := config.Config{
			JWTPrivateKeyPath: filepath.Join(dir, "jwt_private.pem"),
			JWTPublicKeyPath:  filepath.Join(dir, "jwt_public.pem"),
			DevKeysDir:        filepath.Join(dir, "unused"),
		}
		priv, pub, err := loadOrGenerateJWTKeys(cfg, false, discardLogger())
		require.NoError(t, err)
		assert.Equal(t, key.N, priv.N)
		assert.Equal(t, key.PublicKey.N, pub.N)

		_, statErr := os.Stat(filepath.Join(dir, "unused"))
		assert.True(t, os.IsNotExist(statErr), "DevKeysDir must be untouched when explicit key paths are configured")
	})

	t.Run("no paths and ephemeral not allowed -- error", func(t *testing.T) {
		_, _, err := loadOrGenerateJWTKeys(config.Config{}, false, discardLogger())
		assert.ErrorContains(t, err, "required outside AUTH_MODE")
	})

	t.Run("first run generates and persists a keypair under DevKeysDir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "dev-keys")
		cfg := config.Config{DevKeysDir: dir}

		priv, pub, err := loadOrGenerateJWTKeys(cfg, true, discardLogger())
		require.NoError(t, err)
		assert.Equal(t, priv.PublicKey.N, pub.N)

		_, statErr := os.Stat(filepath.Join(dir, "jwt_private.pem"))
		assert.NoError(t, statErr, "the generated keypair must be persisted for the next restart to find")
	})

	t.Run("a second call reuses the persisted keypair instead of generating a new one", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "dev-keys")
		cfg := config.Config{DevKeysDir: dir}

		priv1, _, err := loadOrGenerateJWTKeys(cfg, true, discardLogger())
		require.NoError(t, err)

		priv2, _, err := loadOrGenerateJWTKeys(cfg, true, discardLogger())
		require.NoError(t, err)

		assert.Equal(t, priv1.N, priv2.N, "a token issued before a restart must still verify after it")
	})
}
