package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestUploadKeyService_RecordAndLookup(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewUploadKeyService(pool, repository.NewUploadKeyRepository())

	key := "Alert/2026/01/01/some-alert/" + uuid.NewString() + "_evidence.png"

	t.Run("no row yet -- found is false, not an error", func(t *testing.T) {
		_, _, found, err := svc.Lookup(t.Context(), tenantID, key)
		require.NoError(t, err)
		assert.False(t, found)
	})

	alertID := uuid.New()
	require.NoError(t, svc.Record(t.Context(), tenantID, key, "alert", alertID))

	t.Run("lookup after record", func(t *testing.T) {
		contextType, contextID, found, err := svc.Lookup(t.Context(), tenantID, key)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "alert", contextType)
		assert.Equal(t, alertID, contextID)
	})
}

func TestUploadKeyService_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	svc := service.NewUploadKeyService(pool, repository.NewUploadKeyRepository())

	key := "Alert/2026/01/01/some-alert/" + uuid.NewString() + "_evidence.png"
	require.NoError(t, svc.Record(t.Context(), tenantA, key, "alert", uuid.New()))

	_, _, found, err := svc.Lookup(t.Context(), tenantB, key)
	require.NoError(t, err)
	assert.False(t, found, "RLS must prevent tenant B from seeing tenant A's upload key")
}
