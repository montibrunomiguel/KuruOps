package repository_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestTenantRepository_GetDefault(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	repo := repository.NewTenantRepository()

	// The seed migration guarantees at least one tenant exists in any
	// migrated database (including kuruops_test) -- GetDefault must
	// resolve it without needing app.tenant_id set at all.
	tenant, err := repo.GetDefault(context.Background(), pool)
	require.NoError(t, err)
	require.NotNil(t, tenant)
	assert.NotEqual(t, "", tenant.Name)
	assert.NotEqual(t, "", tenant.Slug)
}

func TestTenantRepository_GetSetTimezone(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewTenantRepository()

	tz, err := repo.GetTimezone(context.Background(), pool, tenantID)
	require.NoError(t, err)
	assert.Equal(t, "UTC", tz, "a freshly created tenant defaults to UTC")

	require.NoError(t, repo.SetTimezone(context.Background(), pool, tenantID, "America/Sao_Paulo"))
	tz, err = repo.GetTimezone(context.Background(), pool, tenantID)
	require.NoError(t, err)
	assert.Equal(t, "America/Sao_Paulo", tz)
}
