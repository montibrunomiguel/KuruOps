package repository_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestSlackConfigRepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "admin", nil)
	repo := repository.NewSlackConfigRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("no config yet returns nil, not an error", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	cfg := &domain.SlackConfig{
		TenantID: tenantID, BotTokenSecretRef: "secret://slack-bot-token",
		TeamID: "T123", TeamName: "Acme Corp", BotUserID: "U456",
		InstalledByUserID: userID, GrantedScopes: "chat:write,channels:read",
	}
	require.NoError(t, repo.Upsert(t.Context(), tx, cfg))

	t.Run("get after connect, including the joined installer name", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "T123", got.TeamID)
		assert.Equal(t, "Acme Corp", got.TeamName)
		assert.Equal(t, "U456", got.BotUserID)
		assert.Equal(t, userID, got.InstalledByUserID)
		assert.Equal(t, "Test User", got.InstalledByUserName)
		assert.Equal(t, "chat:write,channels:read", got.GrantedScopes)
		assert.Equal(t, "secret://slack-bot-token", got.BotTokenSecretRef)
	})

	t.Run("reconnecting overwrites the whole row", func(t *testing.T) {
		otherUserID := testutil.NewUser(t, tenantID, "admin", nil)
		require.NoError(t, repo.Upsert(t.Context(), tx, &domain.SlackConfig{
			TenantID: tenantID, BotTokenSecretRef: "secret://slack-bot-token-2",
			TeamID: "T999", TeamName: "New Workspace", BotUserID: "U789",
			InstalledByUserID: otherUserID, GrantedScopes: "chat:write",
		}))

		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Equal(t, "T999", got.TeamID)
		assert.Equal(t, "New Workspace", got.TeamName)
		assert.Equal(t, otherUserID, got.InstalledByUserID)
	})

	t.Run("delete disconnects the workspace", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx))
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestSlackConfigRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	userA := testutil.NewUser(t, tenantA, "admin", nil)
	repo := repository.NewSlackConfigRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Upsert(t.Context(), txA, &domain.SlackConfig{
		TenantID: tenantA, BotTokenSecretRef: "secret://a",
		TeamID: "T-A", TeamName: "Tenant A Workspace", BotUserID: "U-A",
		InstalledByUserID: userA, GrantedScopes: "chat:write",
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.Get(t.Context(), txB)
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from seeing tenant A's slack config")
}
