package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestTagRepository_CreateListDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewTagRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	color := "#ff0000"
	tag := &domain.Tag{TenantID: tenantID, Name: "phishing", Color: &color}
	require.NoError(t, repo.Create(t.Context(), tx, tag))
	require.NotEqual(t, [16]byte{}, tag.ID)

	t.Run("list returns it sorted by name", func(t *testing.T) {
		require.NoError(t, repo.Create(t.Context(), tx, &domain.Tag{TenantID: tenantID, Name: "abuse"}))
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 2)
		assert.Equal(t, "abuse", list[0].Name)
		assert.Equal(t, "phishing", list[1].Name)
	})

	t.Run("delete removes it and returns the deleted tag's name", func(t *testing.T) {
		name, err := repo.Delete(t.Context(), tx, tag.ID)
		require.NoError(t, err)
		assert.Equal(t, "phishing", name)
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "abuse", list[0].Name)
	})

	t.Run("delete of an unknown id returns an empty name, not an error", func(t *testing.T) {
		name, err := repo.Delete(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.Empty(t, name)
	})
}

func TestTagRepository_FilterKnown(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewTagRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.Create(t.Context(), tx, &domain.Tag{TenantID: tenantID, Name: "phishing"}))
	require.NoError(t, repo.Create(t.Context(), tx, &domain.Tag{TenantID: tenantID, Name: "VPN"}))

	t.Run("returns only registered names, case-insensitively", func(t *testing.T) {
		known, err := repo.FilterKnown(t.Context(), tx, []string{"phishing", "vpn", "random-unknown-tag"})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"phishing", "VPN"}, known)
	})

	t.Run("empty input returns nil without querying", func(t *testing.T) {
		known, err := repo.FilterKnown(t.Context(), tx, nil)
		require.NoError(t, err)
		assert.Nil(t, known)
	})

	t.Run("no matches returns an empty, non-nil slice", func(t *testing.T) {
		known, err := repo.FilterKnown(t.Context(), tx, []string{"totally-unknown"})
		require.NoError(t, err)
		assert.Empty(t, known)
	})
}

func TestTagRepository_EnsureExist(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewTagRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.Create(t.Context(), tx, &domain.Tag{TenantID: tenantID, Name: "phishing"}))

	t.Run("creates any name not already in the catalog, leaves existing ones untouched", func(t *testing.T) {
		require.NoError(t, repo.EnsureExist(t.Context(), tx, tenantID, []string{"phishing", "new-from-siem"}))
		known, err := repo.FilterKnown(t.Context(), tx, []string{"phishing", "new-from-siem"})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"phishing", "new-from-siem"}, known)
	})

	t.Run("is case-insensitively idempotent -- an existing name in different casing doesn't create a duplicate row", func(t *testing.T) {
		require.NoError(t, repo.EnsureExist(t.Context(), tx, tenantID, []string{"PHISHING"}))
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		names := make([]string, len(list))
		for i, tg := range list {
			names[i] = tg.Name
		}
		assert.ElementsMatch(t, []string{"phishing", "new-from-siem"}, names, "still stored as the original \"phishing\", not a second \"PHISHING\" row")
	})

	t.Run("duplicate names within the same call don't error", func(t *testing.T) {
		require.NoError(t, repo.EnsureExist(t.Context(), tx, tenantID, []string{"dup", "dup", "dup"}))
		known, err := repo.FilterKnown(t.Context(), tx, []string{"dup"})
		require.NoError(t, err)
		assert.Equal(t, []string{"dup"}, known)
	})

	t.Run("empty input is a no-op", func(t *testing.T) {
		require.NoError(t, repo.EnsureExist(t.Context(), tx, tenantID, nil))
	})
}

func TestTagRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewTagRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Create(t.Context(), txA, &domain.Tag{TenantID: tenantA, Name: "tenant-a-only"}))

	txB := testutil.BeginTx(t, pool, tenantB)
	list, err := repo.List(t.Context(), txB)
	require.NoError(t, err)
	assert.Empty(t, list, "RLS must prevent tenant B from seeing tenant A's tags")
}
