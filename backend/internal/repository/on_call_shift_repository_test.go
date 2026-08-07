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

func TestOnCallShiftRepository_CreateListDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewOnCallShiftRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	shift := &domain.OnCallShift{TenantID: tenantID, UserID: analystID, Weekday: 1, StartMinute: 540, EndMinute: 1020}
	require.NoError(t, repo.Create(t.Context(), tx, shift))
	require.NotEqual(t, [16]byte{}, shift.ID)

	list, err := repo.List(t.Context(), tx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, analystID, list[0].UserID)
	assert.NotEmpty(t, list[0].UserName, "List must resolve the analyst's name via the join")
	assert.Equal(t, 1, list[0].Weekday)
	assert.Equal(t, 540, list[0].StartMinute)
	assert.Equal(t, 1020, list[0].EndMinute)

	require.NoError(t, repo.Delete(t.Context(), tx, shift.ID))
	list, err = repo.List(t.Context(), tx)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestOnCallShiftRepository_ResolveCurrentAnalyst(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewOnCallShiftRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("no shifts configured -- no match", func(t *testing.T) {
		got, err := repo.ResolveCurrentAnalyst(t.Context(), tx, 2, 1, 600)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("non-wrapping shift matches within its range", func(t *testing.T) {
		// Tuesday 09:00-17:00 (540-1020 minutes).
		shift := &domain.OnCallShift{TenantID: tenantID, UserID: analystID, Weekday: 2, StartMinute: 540, EndMinute: 1020}
		require.NoError(t, repo.Create(t.Context(), tx, shift))

		got, err := repo.ResolveCurrentAnalyst(t.Context(), tx, 2, 1, 600) // Tuesday 10:00
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, analystID, *got)

		got, err = repo.ResolveCurrentAnalyst(t.Context(), tx, 2, 1, 1021) // Tuesday 17:01 -- just past the end
		require.NoError(t, err)
		assert.Nil(t, got)

		got, err = repo.ResolveCurrentAnalyst(t.Context(), tx, 3, 2, 600) // Wednesday 10:00 -- wrong weekday
		require.NoError(t, err)
		assert.Nil(t, got)

		require.NoError(t, repo.Delete(t.Context(), tx, shift.ID))
	})

	t.Run("wrapping shift matches both the evening half and the early-morning bleed-over", func(t *testing.T) {
		// Friday 22:00 (1320) -- Saturday 06:00 (360).
		require.NoError(t, repo.Create(t.Context(), tx, &domain.OnCallShift{TenantID: tenantID, UserID: analystID, Weekday: 5, StartMinute: 1320, EndMinute: 360}))

		got, err := repo.ResolveCurrentAnalyst(t.Context(), tx, 5, 4, 1380) // Friday 23:00 -- evening half
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, analystID, *got)

		got, err = repo.ResolveCurrentAnalyst(t.Context(), tx, 6, 5, 120) // Saturday 02:00 -- bled past midnight
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, analystID, *got)

		got, err = repo.ResolveCurrentAnalyst(t.Context(), tx, 6, 5, 420) // Saturday 07:00 -- already ended
		require.NoError(t, err)
		assert.Nil(t, got)

		got, err = repo.ResolveCurrentAnalyst(t.Context(), tx, 5, 4, 1200) // Friday 20:00 -- before it starts
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("two overlapping shifts split assignments randomly, not always the same analyst", func(t *testing.T) {
		analystB := testutil.NewUser(t, tenantID, "analyst", nil)
		// Both on Wednesday 09:00-17:00 (540-1020) -- full overlap, backup coverage.
		shiftA := &domain.OnCallShift{TenantID: tenantID, UserID: analystID, Weekday: 3, StartMinute: 540, EndMinute: 1020}
		shiftB := &domain.OnCallShift{TenantID: tenantID, UserID: analystB, Weekday: 3, StartMinute: 540, EndMinute: 1020}
		require.NoError(t, repo.Create(t.Context(), tx, shiftA))
		require.NoError(t, repo.Create(t.Context(), tx, shiftB))

		seen := map[uuid.UUID]bool{}
		for i := 0; i < 40; i++ {
			got, err := repo.ResolveCurrentAnalyst(t.Context(), tx, 3, 2, 600) // Wednesday 10:00
			require.NoError(t, err)
			require.NotNil(t, got)
			seen[*got] = true
		}
		assert.Len(t, seen, 2, "40 resolutions against two fully-overlapping shifts must hit both analysts, not just one")

		require.NoError(t, repo.Delete(t.Context(), tx, shiftA.ID))
		require.NoError(t, repo.Delete(t.Context(), tx, shiftB.ID))
	})
}

func TestOnCallShiftRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	analystA := testutil.NewUser(t, tenantA, "analyst", nil)
	repo := repository.NewOnCallShiftRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Create(t.Context(), txA, &domain.OnCallShift{TenantID: tenantA, UserID: analystA, Weekday: 1, StartMinute: 0, EndMinute: 60}))

	txB := testutil.BeginTx(t, pool, tenantB)
	list, err := repo.List(t.Context(), txB)
	require.NoError(t, err)
	assert.Empty(t, list, "RLS must prevent tenant B from seeing tenant A's on-call shifts")
}
