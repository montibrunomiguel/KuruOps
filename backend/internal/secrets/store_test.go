package secrets_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/secrets"
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
