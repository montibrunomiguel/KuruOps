package secrets_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/secrets"
)

func TestEnvStore_PutAndResolve(t *testing.T) {
	store := secrets.NewEnvStore()

	ref, err := store.Put(t.Context(), "tenant-1", "llm:openai", "sk-secret-value")
	require.NoError(t, err)
	assert.NotContains(t, ref, "sk-secret-value", "the ref must not embed the raw secret value")

	value, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, "sk-secret-value", value)
}

func TestEnvStore_ResolveUnknownRef(t *testing.T) {
	store := secrets.NewEnvStore()
	value, err := store.Resolve(t.Context(), "no-such-ref")
	require.NoError(t, err)
	assert.Empty(t, value)
}

func TestEnvStore_SamePurposeDifferentTenantsDoNotCollide(t *testing.T) {
	store := secrets.NewEnvStore()

	ref1, err := store.Put(t.Context(), "tenant-1", "llm:openai", "secret-for-tenant-1")
	require.NoError(t, err)
	ref2, err := store.Put(t.Context(), "tenant-2", "llm:openai", "secret-for-tenant-2")
	require.NoError(t, err)

	assert.NotEqual(t, ref1, ref2)

	v1, err := store.Resolve(t.Context(), ref1)
	require.NoError(t, err)
	assert.Equal(t, "secret-for-tenant-1", v1)

	v2, err := store.Resolve(t.Context(), ref2)
	require.NoError(t, err)
	assert.Equal(t, "secret-for-tenant-2", v2)
}

// failingStore's Put always errors -- used only to exercise
// PutOrKeepExisting's own error-wrapping branch, which EnvStore (whose Put
// never fails) can't reach.
type failingStore struct{}

func (failingStore) Put(ctx context.Context, tenantID, purpose, value string) (string, error) {
	return "", errors.New("boom")
}

func (failingStore) Resolve(ctx context.Context, ref string) (string, error) {
	return "", nil
}

func TestPutOrKeepExisting(t *testing.T) {
	t.Run("empty plaintext keeps the existing ref unchanged", func(t *testing.T) {
		store := secrets.NewEnvStore()
		got, err := secrets.PutOrKeepExisting(t.Context(), store, "tenant-1", "smtp:password", "", "existing-ref")
		require.NoError(t, err)
		assert.Equal(t, "existing-ref", got)
	})

	t.Run("empty plaintext with no existing ref stays empty", func(t *testing.T) {
		store := secrets.NewEnvStore()
		got, err := secrets.PutOrKeepExisting(t.Context(), store, "tenant-1", "smtp:password", "", "")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("non-empty plaintext is stored fresh, replacing any existing ref", func(t *testing.T) {
		store := secrets.NewEnvStore()
		got, err := secrets.PutOrKeepExisting(t.Context(), store, "tenant-1", "smtp:password", "new-secret", "stale-ref")
		require.NoError(t, err)
		assert.NotEqual(t, "stale-ref", got)

		value, err := store.Resolve(t.Context(), got)
		require.NoError(t, err)
		assert.Equal(t, "new-secret", value)
	})

	t.Run("a store failure is wrapped, not swallowed", func(t *testing.T) {
		_, err := secrets.PutOrKeepExisting(t.Context(), failingStore{}, "tenant-1", "smtp:password", "new-secret", "existing-ref")
		assert.ErrorContains(t, err, "store secret")
		assert.ErrorContains(t, err, "boom")
	})
}

// TestEnvStore_ConcurrentAccess exists to be run under `go test -race`:
// EnvStore is one shared instance across every request (see its doc
// comment), so concurrent Put/Resolve calls from different goroutines is
// the normal, expected access pattern, not an edge case.
func TestEnvStore_ConcurrentAccess(t *testing.T) {
	store := secrets.NewEnvStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tenantID := fmt.Sprintf("tenant-%d", i)
			ref, err := store.Put(t.Context(), tenantID, "llm:openai", "secret")
			assert.NoError(t, err)
			_, err = store.Resolve(t.Context(), ref)
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()
}
