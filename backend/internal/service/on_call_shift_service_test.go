package service_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newOnCallShiftService(t *testing.T) *service.OnCallShiftService {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	return service.NewOnCallShiftService(pool, repository.NewOnCallShiftRepository(), repository.NewUserRepository(), repository.NewTenantRepository())
}

func TestOnCallShiftService_CreateListDelete(t *testing.T) {
	svc := newOnCallShiftService(t)
	tenantID := testutil.NewTenant(t)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("rejects an out-of-range weekday", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, analystID, 7, 0, 60)
		assert.ErrorContains(t, err, "weekday")
	})

	t.Run("rejects an out-of-range minute", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, analystID, 1, -1, 60)
		assert.ErrorContains(t, err, "minute")
	})

	t.Run("rejects a zero-length shift", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, analystID, 1, 60, 60)
		assert.ErrorContains(t, err, "zero minutes")
	})

	t.Run("rejects an unknown analyst", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, uuid.New(), 1, 0, 60)
		assert.ErrorContains(t, err, "not found")
	})

	shift, err := svc.Create(t.Context(), tenantID, analystID, 1, 540, 1020)
	require.NoError(t, err)
	assert.Equal(t, analystID, shift.UserID)
	assert.NotEmpty(t, shift.UserName)

	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, svc.Delete(t.Context(), tenantID, shift.ID))
	list, err = svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestOnCallShiftService_Timezone(t *testing.T) {
	svc := newOnCallShiftService(t)
	tenantID := testutil.NewTenant(t)

	tz, err := svc.GetTimezone(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "UTC", tz)

	t.Run("rejects an unknown IANA timezone", func(t *testing.T) {
		err := svc.SetTimezone(t.Context(), tenantID, "Not/A_Real_Zone")
		assert.ErrorContains(t, err, "unknown timezone")
	})

	require.NoError(t, svc.SetTimezone(t.Context(), tenantID, "America/Sao_Paulo"))
	tz, err = svc.GetTimezone(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "America/Sao_Paulo", tz)
}

func TestOnCallShiftService_ResolveCurrentAnalyst(t *testing.T) {
	svc := newOnCallShiftService(t)
	tenantID := testutil.NewTenant(t)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("no shifts -- nil, no error", func(t *testing.T) {
		got, err := svc.ResolveCurrentAnalyst(t.Context(), tenantID, time.Now())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("resolves using the tenant's configured timezone, not UTC", func(t *testing.T) {
		require.NoError(t, svc.SetTimezone(t.Context(), tenantID, "America/Sao_Paulo")) // UTC-3, no DST
		// Wednesday 09:00-17:00 America/Sao_Paulo local time.
		_, err := svc.Create(t.Context(), tenantID, analystID, int(time.Wednesday), 540, 1020)
		require.NoError(t, err)

		// 2024-01-03 is a Wednesday. 13:00 UTC = 10:00 America/Sao_Paulo --
		// inside the shift in local time, even though 13:00 UTC alone would
		// suggest otherwise if timezone conversion were skipped.
		now := time.Date(2024, 1, 3, 13, 0, 0, 0, time.UTC)
		got, err := svc.ResolveCurrentAnalyst(t.Context(), tenantID, now)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, analystID, *got)

		// 23:00 UTC on the same Wednesday = 20:00 local -- past the shift's
		// 17:00 local end.
		afterShift := time.Date(2024, 1, 3, 23, 0, 0, 0, time.UTC)
		got, err = svc.ResolveCurrentAnalyst(t.Context(), tenantID, afterShift)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
