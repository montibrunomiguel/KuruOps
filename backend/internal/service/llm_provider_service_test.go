package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// fakeLLMProviderRepo lets a test fail a specific repo call on demand --
// LLMProviderService takes an interface (not the concrete
// *repository.LLMProviderRepository) specifically so this is possible. See
// fakeStorageConfigRepo (storage_config_service_test.go) for the fuller
// version of this reasoning.
type fakeLLMProviderRepo struct {
	listErr       error
	getErr        error
	insertErr     error
	updateErr     error
	setDefaultErr error
	deleteErr     error
	get           *domain.LLMProvider
}

func (f *fakeLLMProviderRepo) List(context.Context, pgx.Tx) ([]domain.LLMProvider, error) {
	return nil, f.listErr
}
func (f *fakeLLMProviderRepo) Get(context.Context, pgx.Tx, uuid.UUID) (*domain.LLMProvider, error) {
	return f.get, f.getErr
}
func (f *fakeLLMProviderRepo) Insert(context.Context, pgx.Tx, *domain.LLMProvider) error {
	return f.insertErr
}
func (f *fakeLLMProviderRepo) Update(context.Context, pgx.Tx, *domain.LLMProvider) error {
	return f.updateErr
}
func (f *fakeLLMProviderRepo) SetDefault(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return f.setDefaultErr
}
func (f *fakeLLMProviderRepo) Delete(context.Context, pgx.Tx, uuid.UUID) error { return f.deleteErr }

func TestLLMProviderService_Create(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())

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

// TestLLMProviderService_CreateDefaulting covers the "first one is
// automatically the default" rule -- a fresh tenant per sub-test, since the
// rule is about what a tenant already has.
func TestLLMProviderService_CreateDefaulting(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	newSvc := func() *service.LLMProviderService {
		return service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
	}

	t.Run("the first provider a tenant registers becomes the default", func(t *testing.T) {
		tenantID := testutil.NewTenant(t)
		actorID := testutil.NewUser(t, tenantID, "admin", nil)
		svc := newSvc()

		first, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "First", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-1",
		})
		require.NoError(t, err)
		assert.True(t, first.IsDefault, "nothing else exists, so this one has to be usable without a second click")
	})

	t.Run("a second provider does not steal the default", func(t *testing.T) {
		tenantID := testutil.NewTenant(t)
		actorID := testutil.NewUser(t, tenantID, "admin", nil)
		svc := newSvc()

		_, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "First", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-1",
		})
		require.NoError(t, err)

		second, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "Second", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-2",
		})
		require.NoError(t, err)
		assert.False(t, second.IsDefault, "an existing default must not be silently reassigned")
	})

	t.Run("deleting the default leaves the next created provider to take it", func(t *testing.T) {
		// Delete promotes nobody, so without this rule a tenant can end up
		// holding providers with no default at all -- and every analysis
		// keeps failing with "no LLM provider configured".
		tenantID := testutil.NewTenant(t)
		actorID := testutil.NewUser(t, tenantID, "admin", nil)
		svc := newSvc()

		first, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "First", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-1",
		})
		require.NoError(t, err)
		require.True(t, first.IsDefault)
		require.NoError(t, svc.Delete(t.Context(), tenantID, actorID, first.ID))

		replacement, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
			Name: "Replacement", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-2",
		})
		require.NoError(t, err)
		assert.True(t, replacement.IsDefault, "no default existed, so the new provider must become one")
	})
}

func TestLLMProviderService_Update(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	store := secrets.NewEnvStore()
	svc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store, repository.NewAdminAuditEventRepository())

	p, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "OpenAI", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-original",
	})
	require.NoError(t, err)
	originalRef := p.APIKeySecretRef

	t.Run("empty APIKey on update keeps the existing secret ref", func(t *testing.T) {
		updated, err := svc.Update(t.Context(), tenantID, actorID, p.ID, service.LLMProviderSaveInput{
			Name: "OpenAI Renamed", Kind: "openai_compatible", Model: "gpt-4o-mini", APIKey: "",
		})
		require.NoError(t, err)
		assert.Equal(t, "OpenAI Renamed", updated.Name)
		assert.Equal(t, originalRef, updated.APIKeySecretRef)
	})

	t.Run("updating a nonexistent provider fails", func(t *testing.T) {
		_, err := svc.Update(t.Context(), tenantID, actorID, uuid.New(), service.LLMProviderSaveInput{
			Name: "x", Kind: "openai_compatible", Model: "m",
		})
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("AutoAnalyzeAllAlerts fully replaces on every update, same as Name/Kind/Model", func(t *testing.T) {
		updated, err := svc.Update(t.Context(), tenantID, actorID, p.ID, service.LLMProviderSaveInput{
			Name: "OpenAI", Kind: "openai_compatible", Model: "gpt-4o", AutoAnalyzeAllAlerts: true,
		})
		require.NoError(t, err)
		assert.True(t, updated.AutoAnalyzeAllAlerts)

		updated, err = svc.Update(t.Context(), tenantID, actorID, p.ID, service.LLMProviderSaveInput{
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
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secrets.NewEnvStore(), auditRepo)

	p, err := svc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "OpenAI", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-1",
	})
	require.NoError(t, err)

	require.NoError(t, svc.SetDefault(t.Context(), tenantID, actorID, p.ID))
	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].IsDefault)

	require.NoError(t, svc.Delete(t.Context(), tenantID, actorID, p.ID))
	list, err = svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)

	t.Run("create/set-default/delete each record an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "ai-integration", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "create")
		assert.Contains(t, actions, "set-default")
		assert.Contains(t, actions, "delete")
	})
}

// TestLLMProviderService_RepoErrors exercises each mutating method's "load
// existing provider to build the audit diff, then persist" error-wrapping
// branches -- unreachable via a real Postgres integration test.
func TestLLMProviderService_RepoErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	validInput := service.LLMProviderSaveInput{Name: "X", Kind: "openai_compatible", Model: "gpt-4o", APIKey: "sk-test"}

	t.Run("Create wraps an Insert failure", func(t *testing.T) {
		svc := service.NewLLMProviderService(pool, &fakeLLMProviderRepo{insertErr: errors.New("insert boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		_, err := svc.Create(t.Context(), tenantID, actorID, validInput)
		assert.ErrorContains(t, err, "insert boom")
	})

	t.Run("Update wraps a load-existing failure", func(t *testing.T) {
		svc := service.NewLLMProviderService(pool, &fakeLLMProviderRepo{getErr: errors.New("get boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		_, err := svc.Update(t.Context(), tenantID, actorID, uuid.New(), validInput)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Update wraps an Update failure", func(t *testing.T) {
		svc := service.NewLLMProviderService(pool, &fakeLLMProviderRepo{get: &domain.LLMProvider{}, updateErr: errors.New("update boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		_, err := svc.Update(t.Context(), tenantID, actorID, uuid.New(), validInput)
		assert.ErrorContains(t, err, "update boom")
	})

	t.Run("SetDefault wraps a SetDefault failure", func(t *testing.T) {
		svc := service.NewLLMProviderService(pool, &fakeLLMProviderRepo{setDefaultErr: errors.New("set-default boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		err := svc.SetDefault(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "set-default boom")
	})

	t.Run("Delete wraps a Get failure", func(t *testing.T) {
		svc := service.NewLLMProviderService(pool, &fakeLLMProviderRepo{getErr: errors.New("get boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		err := svc.Delete(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Delete wraps a Delete failure", func(t *testing.T) {
		svc := service.NewLLMProviderService(pool, &fakeLLMProviderRepo{deleteErr: errors.New("delete boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		err := svc.Delete(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "delete boom")
	})
}
