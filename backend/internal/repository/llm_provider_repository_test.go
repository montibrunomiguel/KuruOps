package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestLLMProviderRepository_InsertGetListUpdateDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewLLMProviderRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	baseURL := "https://api.openai.com/v1"
	p := &domain.LLMProvider{
		TenantID:        tenantID,
		Name:            "OpenAI GPT-4",
		Kind:            "openai_compatible",
		BaseURL:         &baseURL,
		Model:           "gpt-4o",
		APIKeySecretRef: "secret://llm-key",
	}
	require.NoError(t, repo.Insert(t.Context(), tx, p))
	require.NotEqual(t, [16]byte{}, p.ID)
	assert.False(t, p.IsDefault)

	t.Run("get", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, p.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "OpenAI GPT-4", got.Name)
	})

	t.Run("get unknown id returns nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	p2 := &domain.LLMProvider{
		TenantID:        tenantID,
		Name:            "Local vLLM",
		Kind:            "openai_compatible",
		Model:           "llama-3",
		APIKeySecretRef: "secret://llm-key-2",
	}
	require.NoError(t, repo.Insert(t.Context(), tx, p2))

	t.Run("list returns every provider", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("update", func(t *testing.T) {
		p.Name = "OpenAI GPT-4o Renamed"
		p.Model = "gpt-4o-mini"
		require.NoError(t, repo.Update(t.Context(), tx, p))

		got, err := repo.Get(t.Context(), tx, p.ID)
		require.NoError(t, err)
		assert.Equal(t, "OpenAI GPT-4o Renamed", got.Name)
		assert.Equal(t, "gpt-4o-mini", got.Model)
	})

	t.Run("set default clears any other default for the tenant", func(t *testing.T) {
		require.NoError(t, repo.SetDefault(t.Context(), tx, tenantID, p.ID))
		got, err := repo.Get(t.Context(), tx, p.ID)
		require.NoError(t, err)
		assert.True(t, got.IsDefault)

		require.NoError(t, repo.SetDefault(t.Context(), tx, tenantID, p2.ID))
		gotP, err := repo.Get(t.Context(), tx, p.ID)
		require.NoError(t, err)
		assert.False(t, gotP.IsDefault, "setting a new default must clear the previous one")

		gotP2, err := repo.Get(t.Context(), tx, p2.ID)
		require.NoError(t, err)
		assert.True(t, gotP2.IsDefault)
	})

	t.Run("get default returns the currently-default provider", func(t *testing.T) {
		got, err := repo.GetDefault(t.Context(), tx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, p2.ID, got.ID)
	})

	t.Run("delete", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx, p.ID))
		got, err := repo.Get(t.Context(), tx, p.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestLLMProviderRepository_GetDefault_None(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewLLMProviderRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	got, err := repo.GetDefault(t.Context(), tx)
	require.NoError(t, err)
	assert.Nil(t, got, "no provider has been marked default yet")
}
