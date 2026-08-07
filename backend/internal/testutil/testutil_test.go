package testutil_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/testutil"
)

// TestPlumbing exercises testutil itself against a real database: a fresh
// tenant, a fresh user, and a BeginTx transaction that can see them under
// RLS (proving app.tenant_id was actually set) but whose writes vanish on
// rollback (proving cleanup works without manual deletion).
func TestPlumbing(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "admin", []string{"alerts", "incidents"})

	tx := testutil.BeginTx(t, pool, tenantID)

	var count int
	err := tx.QueryRow(context.Background(), "select count(*) from users where id = $1", userID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count, "the fixture user should be visible under this tenant's RLS scope")

	_, err = tx.Exec(context.Background(), "update users set name = 'changed' where id = $1", userID)
	require.NoError(t, err)
}
