package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestLLMProviderService_Create(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secrets.NewEnvStore())

	t.Run("anthropic rejects a custom base_url", func(t *testing.T) {
		baseURL := "https://sketchy-proxy.example.com"
		_, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "Claude", Kind: "anthropic", BaseURL: &baseURL, Model: "claude-opus", APIKey: "sk-test",
		})
		assert.ErrorContains(t, err, "does not accept a custom base_url")
	})

	t.Run("unknown kind is rejected", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "Mystery", Kind: "made-up-kind", Model: "m", APIKey: "k",
		})
		assert.ErrorContains(t, err, "unknown llm provider kind")
	})

	t.Run("the plaintext API key never reaches the stored record", func(t *testing.T) {
		p, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "OpenAI", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-super-secret",
		})
		require.NoError(t, err)
		assert.NotContains(t, p.APIKeySecretRef, "sk-super-secret")
	})

	t.Run("AutoAnalyzeAllAlerts defaults off, opts in when set", func(t *testing.T) {
		off, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "Default Off", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-1",
		})
		require.NoError(t, err)
		assert.False(t, off.AutoAnalyzeAllAlerts)

		on, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "Opted In", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-2", AutoAnalyzeAllAlerts: true,
		})
		require.NoError(t, err)
		assert.True(t, on.AutoAnalyzeAllAlerts)
	})
}

func TestLLMProviderService_Update(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	store := secrets.NewEnvStore()
	svc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store)

	p, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "OpenAI", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-original",
	})
	require.NoError(t, err)
	originalRef := p.APIKeySecretRef

	t.Run("empty APIKey on update keeps the existing secret ref", func(t *testing.T) {
		updated, err := svc.Update(t.Context(), tenantID, p.ID, service.LLMProviderSaveInput{
			Name: "OpenAI Renamed", Kind: "openai_compatible", Model: "gpt-4o-mini", APIKey: "",
		})
		require.NoError(t, err)
		assert.Equal(t, "OpenAI Renamed", updated.Name)
		assert.Equal(t, originalRef, updated.APIKeySecretRef)
	})

	t.Run("updating a nonexistent provider fails", func(t *testing.T) {
		_, err := svc.Update(t.Context(), tenantID, uuid.New(), service.LLMProviderSaveInput{
			Name: "x", Kind: "openai_compatible", Model: "m",
		})
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("AutoAnalyzeAllAlerts fully replaces on every update, same as Name/Kind/Model", func(t *testing.T) {
		updated, err := svc.Update(t.Context(), tenantID, p.ID, service.LLMProviderSaveInput{
			Name: "OpenAI", Kind: "openai_compatible", Model: "gpt-4o", AutoAnalyzeAllAlerts: true,
		})
		require.NoError(t, err)
		assert.True(t, updated.AutoAnalyzeAllAlerts)

		updated, err = svc.Update(t.Context(), tenantID, p.ID, service.LLMProviderSaveInput{
			Name: "OpenAI", Kind: "openai_compatible", Model: "gpt-4o", AutoAnalyzeAllAlerts: false,
		})
		require.NoError(t, err)
		assert.False(t, updated.AutoAnalyzeAllAlerts)
	})
}

func TestLLMProviderService_SetDefaultAndDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secrets.NewEnvStore())

	p, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "OpenAI", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-1",
	})
	require.NoError(t, err)

	require.NoError(t, svc.SetDefault(t.Context(), tenantID, p.ID))
	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].IsDefault)

	require.NoError(t, svc.Delete(t.Context(), tenantID, p.ID))
	list, err = svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)
}
