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

func TestFieldMappingTemplateRepository_InsertGetListDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewFieldMappingTemplateRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	template := &domain.FieldMappingTemplate{
		TenantID: tenantID,
		Name:     "Wazuh fields",
		Rules: []domain.FieldMappingRule{
			{JSONPath: "rule.level", Label: "Rule Level"},
			{JSONPath: "agent.name", Label: "Agent"},
		},
	}
	require.NoError(t, repo.Insert(t.Context(), tx, template))
	require.NotEqual(t, uuid.Nil, template.ID)

	t.Run("get round-trips the rules jsonb", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, template.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Wazuh fields", got.Name)
		assert.Equal(t, template.Rules, got.Rules)
	})

	t.Run("get on an unknown id returns nil, not an error", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("list returns it sorted by name", func(t *testing.T) {
		require.NoError(t, repo.Insert(t.Context(), tx, &domain.FieldMappingTemplate{TenantID: tenantID, Name: "Abuse fields"}))
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 2)
		assert.Equal(t, "Abuse fields", list[0].Name)
		assert.Equal(t, "Wazuh fields", list[1].Name)
	})

	t.Run("update replaces name and rules", func(t *testing.T) {
		template.Name = "Wazuh fields v2"
		template.Rules = []domain.FieldMappingRule{{JSONPath: "rule.groups", Label: "Categories"}}
		require.NoError(t, repo.Update(t.Context(), tx, template))

		got, err := repo.Get(t.Context(), tx, template.ID)
		require.NoError(t, err)
		assert.Equal(t, "Wazuh fields v2", got.Name)
		assert.Equal(t, template.Rules, got.Rules)
	})

	t.Run("delete removes it", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx, template.ID))
		got, err := repo.Get(t.Context(), tx, template.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestFieldMappingTemplateRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewFieldMappingTemplateRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Insert(t.Context(), txA, &domain.FieldMappingTemplate{TenantID: tenantA, Name: "tenant-a-only"}))

	txB := testutil.BeginTx(t, pool, tenantB)
	list, err := repo.List(t.Context(), txB)
	require.NoError(t, err)
	assert.Empty(t, list, "RLS must prevent tenant B from seeing tenant A's field mapping templates")
}
