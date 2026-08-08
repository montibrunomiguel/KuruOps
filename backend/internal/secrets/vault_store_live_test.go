package secrets_test

// This file is the real counterpart to vault_store_test.go's httptest fake:
// it exercises VaultStore against an actual HashiCorp Vault dev-mode server
// (see docker-compose.yml's vault-dev service / `task backend:test:vault`),
// so the KV v2 request/response shapes are validated against the real API,
// not just this codebase's own understanding of it. Gated behind
// TEST_VAULT_ADDR the same way internal/dbmigrate gates its external-target
// test behind TEST_MIGRATION_TARGET_ENABLED -- the default
// backend:test:integration run shouldn't require a Vault container to be up.

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/secrets"
)

func skipUnlessVaultLive(t *testing.T) (addr, token string) {
	t.Helper()
	addr = os.Getenv("TEST_VAULT_ADDR")
	if addr == "" {
		t.Skip("TEST_VAULT_ADDR not set -- run via `task backend:test:vault`")
	}
	token = os.Getenv("TEST_VAULT_TOKEN")
	if token == "" {
		token = "dev-root-token" // matches vault-dev's VAULT_DEV_ROOT_TOKEN_ID
	}
	return addr, token
}

func TestVaultStore_Live_PutAndResolve(t *testing.T) {
	addr, token := skipUnlessVaultLive(t)
	store := secrets.NewVaultStore(addr, token, "secret")

	// A fresh, random tenant/purpose per run so repeated runs against the
	// same (persistent, if not dev-mode) Vault don't collide.
	tenantID := uuid.New().String()
	const plaintext = "sk-live-vault-round-trip-test"

	ref, err := store.Put(t.Context(), tenantID, "llm:openai", plaintext)
	require.NoError(t, err)
	assert.Equal(t, tenantID+"/llm:openai", ref, "matches url.PathEscape's behavior for ':' -- allowed unescaped in a path segment per RFC 3986")

	value, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, plaintext, value, "must decrypt/round-trip through a real Vault server, not just this codebase's mock of one")

	t.Run("overwriting the same ref creates a new KV version, resolve sees the latest", func(t *testing.T) {
		const updated = "sk-live-vault-updated-value"
		ref2, err := store.Put(t.Context(), tenantID, "llm:openai", updated)
		require.NoError(t, err)
		assert.Equal(t, ref, ref2, "same tenant+purpose must reuse the same path")

		value, err := store.Resolve(t.Context(), ref2)
		require.NoError(t, err)
		assert.Equal(t, updated, value)
	})
}

func TestVaultStore_Live_ResolveNotFound(t *testing.T) {
	addr, token := skipUnlessVaultLive(t)
	store := secrets.NewVaultStore(addr, token, "secret")

	_, err := store.Resolve(t.Context(), uuid.New().String()+"/no-such-secret")
	assert.Error(t, err)
}

func TestVaultStore_Live_WrongToken(t *testing.T) {
	addr, _ := skipUnlessVaultLive(t)
	store := secrets.NewVaultStore(addr, "not-the-real-token", "secret")

	_, err := store.Put(t.Context(), uuid.New().String(), "llm:openai", "value")
	assert.Error(t, err, "a real Vault server must reject an invalid token, not silently accept it")
}
