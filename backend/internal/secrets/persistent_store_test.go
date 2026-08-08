package secrets_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/testutil"
)

// testEncryptionKey is the base64 of 32 arbitrary bytes -- any valid
// AES-256 key works for these tests, it doesn't need to look random.
const testEncryptionKey = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="

func TestPersistentEnvStore_PutAndResolve(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	ctx := context.Background()

	store, err := secrets.NewPersistentEnvStore(ctx, pool, testEncryptionKey)
	require.NoError(t, err)

	ref, err := store.Put(ctx, "tenant-1", "ldap-bind-password", "hunter2")
	require.NoError(t, err)

	value, err := store.Resolve(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, "hunter2", value)
}

// TestPersistentEnvStore_SurvivesRestart is the actual bug this type fixes:
// EnvStore's in-memory map lost every secret whenever the process
// restarted while the Postgres row referencing it (ref string) kept
// pointing at the now-empty value. A second store built against the same
// pool must see what the first one wrote.
func TestPersistentEnvStore_SurvivesRestart(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	ctx := context.Background()

	first, err := secrets.NewPersistentEnvStore(ctx, pool, testEncryptionKey)
	require.NoError(t, err)
	ref, err := first.Put(ctx, "tenant-restart", "saml-sp-key", "-----BEGIN RSA PRIVATE KEY-----fake-----END-----")
	require.NoError(t, err)

	second, err := secrets.NewPersistentEnvStore(ctx, pool, testEncryptionKey)
	require.NoError(t, err)
	value, err := second.Resolve(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, "-----BEGIN RSA PRIVATE KEY-----fake-----END-----", value)
}

func TestPersistentEnvStore_ResolveUnknownRef(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	ctx := context.Background()

	store, err := secrets.NewPersistentEnvStore(ctx, pool, testEncryptionKey)
	require.NoError(t, err)

	value, err := store.Resolve(ctx, "no-such-ref")
	require.NoError(t, err)
	assert.Empty(t, value)
}

func TestPersistentEnvStore_RejectsInvalidKey(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	ctx := context.Background()

	_, err := secrets.NewPersistentEnvStore(ctx, pool, "not-valid-base64!!!")
	assert.ErrorContains(t, err, "decode SECRETS_ENCRYPTION_KEY")

	_, err = secrets.NewPersistentEnvStore(ctx, pool, "dG9vc2hvcnQ=")
	assert.ErrorContains(t, err, "32 bytes")
}

func TestPersistentEnvStore_PutOverwritesExistingRef(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	ctx := context.Background()

	store, err := secrets.NewPersistentEnvStore(ctx, pool, testEncryptionKey)
	require.NoError(t, err)

	ref, err := store.Put(ctx, "tenant-overwrite", "llm-api-key", "sk-old")
	require.NoError(t, err)
	_, err = store.Put(ctx, "tenant-overwrite", "llm-api-key", "sk-new")
	require.NoError(t, err)

	value, err := store.Resolve(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, "sk-new", value)

	reloaded, err := secrets.NewPersistentEnvStore(ctx, pool, testEncryptionKey)
	require.NoError(t, err)
	value, err = reloaded.Resolve(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, "sk-new", value)
}
