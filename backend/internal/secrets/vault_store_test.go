package secrets_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/secrets"
)

func TestVaultStore_PutAndResolve(t *testing.T) {
	stored := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-token", r.Header.Get("X-Vault-Token"))
		const prefix = "/v1/secret/data/"
		require.True(t, len(r.URL.Path) > len(prefix) && r.URL.Path[:len(prefix)] == prefix)
		path := r.URL.Path[len(prefix):]

		switch r.Method {
		case http.MethodPost:
			var body struct {
				Data map[string]string `json:"data"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			stored[path] = body.Data["value"]
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 1}})
		case http.MethodGet:
			value, ok := stored[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"data": map[string]string{"value": value}},
			})
		}
	}))
	defer srv.Close()

	store := secrets.NewVaultStore(srv.URL, "test-token", "secret")

	ref, err := store.Put(t.Context(), "tenant-1", "llm:openai", "sk-secret-value")
	require.NoError(t, err)
	assert.Equal(t, "tenant-1/llm:openai", ref)

	value, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, "sk-secret-value", value)
}

func TestVaultStore_Resolve_EmptyRef(t *testing.T) {
	store := secrets.NewVaultStore("http://unused.invalid", "tok", "secret")
	value, err := store.Resolve(t.Context(), "")
	require.NoError(t, err)
	assert.Empty(t, value)
}

func TestVaultStore_Resolve_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[]}`))
	}))
	defer srv.Close()

	store := secrets.NewVaultStore(srv.URL, "tok", "secret")
	_, err := store.Resolve(t.Context(), "tenant-1/no-such-secret")
	assert.ErrorContains(t, err, "404")
}

func TestVaultStore_Put_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
	}))
	defer srv.Close()

	store := secrets.NewVaultStore(srv.URL, "bad-token", "secret")
	_, err := store.Put(t.Context(), "tenant-1", "llm:openai", "value")
	assert.ErrorContains(t, err, "permission denied")
}

func TestVaultStore_SamePurposeDifferentTenantsDoNotCollide(t *testing.T) {
	stored := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/v1/secret/data/"
		path := r.URL.Path[len(prefix):]
		switch r.Method {
		case http.MethodPost:
			var body struct {
				Data map[string]string `json:"data"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			stored[path] = body.Data["value"]
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 1}})
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"data": map[string]string{"value": stored[path]}},
			})
		}
	}))
	defer srv.Close()

	store := secrets.NewVaultStore(srv.URL, "tok", "secret")

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
