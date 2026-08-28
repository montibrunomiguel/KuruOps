package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestOAuthStateService(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewOAuthStateService(pool, repository.NewOAuthStateRepository())

	t.Run("a fresh state consumes successfully and round-trips its metadata", func(t *testing.T) {
		state, err := svc.Generate(t.Context(), tenantID, userID, domain.OAuthProviderGDrive, map[string]string{"folderId": "xyz"})
		require.NoError(t, err)
		require.NotEmpty(t, state)

		consumed, err := svc.Consume(t.Context(), tenantID, domain.OAuthProviderGDrive, state)
		require.NoError(t, err)
		require.NotNil(t, consumed)
		assert.Equal(t, userID, consumed.UserID)
		assert.Equal(t, "xyz", consumed.Metadata["folderId"])
	})

	t.Run("nil metadata is fine -- no metadata to carry", func(t *testing.T) {
		state, err := svc.Generate(t.Context(), tenantID, userID, domain.OAuthProviderSlack, nil)
		require.NoError(t, err)

		consumed, err := svc.Consume(t.Context(), tenantID, domain.OAuthProviderSlack, state)
		require.NoError(t, err)
		require.NotNil(t, consumed)
		assert.Empty(t, consumed.Metadata)
	})

	t.Run("consuming the same state twice fails the second time", func(t *testing.T) {
		state, err := svc.Generate(t.Context(), tenantID, userID, domain.OAuthProviderGDrive, nil)
		require.NoError(t, err)

		first, err := svc.Consume(t.Context(), tenantID, domain.OAuthProviderGDrive, state)
		require.NoError(t, err)
		require.NotNil(t, first)

		second, err := svc.Consume(t.Context(), tenantID, domain.OAuthProviderGDrive, state)
		require.NoError(t, err)
		assert.Nil(t, second, "a state token is single-use")
	})

	t.Run("an unknown state consumes to nil, not an error", func(t *testing.T) {
		consumed, err := svc.Consume(t.Context(), tenantID, domain.OAuthProviderGDrive, "not-a-real-token")
		require.NoError(t, err)
		assert.Nil(t, consumed)
	})

	t.Run("consuming with the wrong provider fails even for a real, unconsumed state", func(t *testing.T) {
		state, err := svc.Generate(t.Context(), tenantID, userID, domain.OAuthProviderGDrive, nil)
		require.NoError(t, err)

		consumed, err := svc.Consume(t.Context(), tenantID, domain.OAuthProviderSlack, state)
		require.NoError(t, err)
		assert.Nil(t, consumed)
	})
}
