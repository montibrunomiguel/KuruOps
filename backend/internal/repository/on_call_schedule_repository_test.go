package repository_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestOnCallScheduleRepository_InsertGetRoundTrip(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alice := testutil.NewUser(t, tenantID, "analyst", nil)
	bob := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewOnCallScheduleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	sched := &domain.OnCallSchedule{
		TenantID:         tenantID,
		Name:             "Primary On-Call",
		IsDefault:        true,
		HandoverAt:       handover,
		PeriodDays:       7,
		ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursSpecificTimes,
		Participants: []domain.OnCallParticipant{
			{UserID: alice},
			{UserID: bob},
		},
		WorkingHours: []domain.OnCallWorkingHoursInterval{
			{Weekdays: []int{1, 2, 3, 4, 5}, StartMinute: 9 * 60, EndMinute: 17 * 60},
		},
	}
	require.NoError(t, repo.Insert(t.Context(), tx, sched))
	require.NotEqual(t, uuid.Nil, sched.ID)

	t.Run("get returns participants in order, working hours, is_default, timestamps", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, sched.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Primary On-Call", got.Name)
		assert.True(t, got.IsDefault)
		assert.WithinDuration(t, handover, got.HandoverAt, time.Second)
		assert.Equal(t, 7, got.PeriodDays)
		assert.Equal(t, 1, got.ConcurrentShifts)
		assert.Equal(t, domain.OnCallWorkingHoursSpecificTimes, got.WorkingHoursMode)
		require.Len(t, got.Participants, 2)
		assert.Equal(t, alice, got.Participants[0].UserID)
		assert.Equal(t, bob, got.Participants[1].UserID)
		require.Len(t, got.WorkingHours, 1)
		assert.Equal(t, []int{1, 2, 3, 4, 5}, got.WorkingHours[0].Weekdays)
		assert.Equal(t, 9*60, got.WorkingHours[0].StartMinute)
		assert.Equal(t, 17*60, got.WorkingHours[0].EndMinute)
	})

	t.Run("update replaces participants and working hours wholesale", func(t *testing.T) {
		sched.Participants = []domain.OnCallParticipant{{UserID: bob}, {UserID: alice}}
		sched.WorkingHours = nil
		sched.WorkingHoursMode = domain.OnCallWorkingHoursAllDay
		require.NoError(t, repo.Update(t.Context(), tx, sched))

		got, err := repo.Get(t.Context(), tx, sched.ID)
		require.NoError(t, err)
		require.Len(t, got.Participants, 2)
		assert.Equal(t, bob, got.Participants[0].UserID)
		assert.Equal(t, alice, got.Participants[1].UserID)
		assert.Empty(t, got.WorkingHours)
		assert.Equal(t, domain.OnCallWorkingHoursAllDay, got.WorkingHoursMode)
	})

	t.Run("update does not touch is_default", func(t *testing.T) {
		require.NoError(t, repo.Update(t.Context(), tx, sched))
		got, err := repo.Get(t.Context(), tx, sched.ID)
		require.NoError(t, err)
		assert.True(t, got.IsDefault, "Update must never change is_default -- only SetDefault does")
	})
}

func TestOnCallScheduleRepository_GetUnknownIDReturnsNil(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewOnCallScheduleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	got, err := repo.Get(t.Context(), tx, uuid.New())
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestOnCallScheduleRepository_List(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewOnCallScheduleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("empty tenant returns no schedules", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	require.NoError(t, repo.Insert(t.Context(), tx, &domain.OnCallSchedule{
		TenantID: tenantID, Name: "Zebra Team", HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	}))
	require.NoError(t, repo.Insert(t.Context(), tx, &domain.OnCallSchedule{
		TenantID: tenantID, Name: "Alpha Team", IsDefault: true, HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	}))

	t.Run("returns every schedule for the tenant, ordered by name", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 2)
		assert.Equal(t, "Alpha Team", list[0].Name)
		assert.True(t, list[0].IsDefault)
		assert.Equal(t, "Zebra Team", list[1].Name)
		assert.False(t, list[1].IsDefault)
	})
}

func TestOnCallScheduleRepository_Delete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewOnCallScheduleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	sched := &domain.OnCallSchedule{
		TenantID: tenantID, Name: "Temp", HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	}
	require.NoError(t, repo.Insert(t.Context(), tx, sched))

	require.NoError(t, repo.Delete(t.Context(), tx, sched.ID))
	got, err := repo.Get(t.Context(), tx, sched.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestOnCallScheduleRepository_SetDefault(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewOnCallScheduleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := &domain.OnCallSchedule{TenantID: tenantID, Name: "A", IsDefault: true, HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay}
	b := &domain.OnCallSchedule{TenantID: tenantID, Name: "B", HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay}
	require.NoError(t, repo.Insert(t.Context(), tx, a))
	require.NoError(t, repo.Insert(t.Context(), tx, b))

	require.NoError(t, repo.SetDefault(t.Context(), tx, tenantID, b.ID))

	gotA, err := repo.Get(t.Context(), tx, a.ID)
	require.NoError(t, err)
	gotB, err := repo.Get(t.Context(), tx, b.ID)
	require.NoError(t, err)
	assert.False(t, gotA.IsDefault, "the previous default must be cleared")
	assert.True(t, gotB.IsDefault)
}

func TestOnCallScheduleRepository_GetDefaultForResolution(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alice := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewOnCallScheduleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("not found when no default schedule exists yet", func(t *testing.T) {
		_, _, _, _, _, _, _, found, err := repo.GetDefaultForResolution(t.Context(), tx, tenantID, "2026-01-01")
		require.NoError(t, err)
		assert.False(t, found)
	})

	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	nonDefault := &domain.OnCallSchedule{
		TenantID: tenantID, Name: "Not the default", HandoverAt: handover, PeriodDays: 7, ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	}
	require.NoError(t, repo.Insert(t.Context(), tx, nonDefault))

	t.Run("still not found -- a non-default schedule doesn't count", func(t *testing.T) {
		_, _, _, _, _, _, _, found, err := repo.GetDefaultForResolution(t.Context(), tx, tenantID, "2026-01-08")
		require.NoError(t, err)
		assert.False(t, found)
	})

	sched := &domain.OnCallSchedule{
		TenantID: tenantID, Name: "The default", IsDefault: true, HandoverAt: handover, PeriodDays: 7, ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
		Participants:     []domain.OnCallParticipant{{UserID: alice}},
	}
	require.NoError(t, repo.Insert(t.Context(), tx, sched))

	t.Run("returns the default schedule's rotation config and participants, no override", func(t *testing.T) {
		participants, handoverAt, periodDays, concurrentShifts, mode, _, override, found, err := repo.GetDefaultForResolution(t.Context(), tx, tenantID, "2026-01-08")
		require.NoError(t, err)
		require.True(t, found)
		require.Len(t, participants, 1)
		assert.Equal(t, alice, participants[0].UserID)
		assert.WithinDuration(t, handover, handoverAt, time.Second)
		assert.Equal(t, 7, periodDays)
		assert.Equal(t, 1, concurrentShifts)
		assert.Equal(t, domain.OnCallWorkingHoursAllDay, mode)
		assert.Nil(t, override)
	})

	t.Run("returns the override for the requested date", func(t *testing.T) {
		bob := testutil.NewUser(t, tenantID, "analyst", nil)
		_, err := repo.CreateOverride(t.Context(), tx, tenantID, sched.ID, bob, "2026-01-08", alice)
		require.NoError(t, err)

		_, _, _, _, _, _, override, found, err := repo.GetDefaultForResolution(t.Context(), tx, tenantID, "2026-01-08")
		require.NoError(t, err)
		require.True(t, found)
		require.NotNil(t, override)
		assert.Equal(t, bob, override.UserID)

		_, _, _, _, _, _, override, found, err = repo.GetDefaultForResolution(t.Context(), tx, tenantID, "2026-01-09")
		require.NoError(t, err)
		require.True(t, found)
		assert.Nil(t, override, "the override only applies to its own date")
	})
}

func TestOnCallScheduleRepository_OverrideCreateReplaceDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actor := testutil.NewUser(t, tenantID, "analyst", nil)
	alice := testutil.NewUser(t, tenantID, "analyst", nil)
	bob := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewOnCallScheduleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	sched := &domain.OnCallSchedule{
		TenantID:         tenantID,
		Name:             "Primary On-Call",
		HandoverAt:       time.Now(),
		PeriodDays:       7,
		ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	}
	require.NoError(t, repo.Insert(t.Context(), tx, sched))

	id, err := repo.CreateOverride(t.Context(), tx, tenantID, sched.ID, alice, "2026-02-01", actor)
	require.NoError(t, err)

	t.Run("creating a second override for the same date replaces the first", func(t *testing.T) {
		replacedID, err := repo.CreateOverride(t.Context(), tx, tenantID, sched.ID, bob, "2026-02-01", actor)
		require.NoError(t, err)
		assert.Equal(t, id, replacedID, "same schedule+date conflict, upsert keeps the same row id")

		list, err := repo.ListOverrides(t.Context(), tx, sched.ID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, bob, list[0].UserID)
	})

	t.Run("delete removes it", func(t *testing.T) {
		require.NoError(t, repo.DeleteOverride(t.Context(), tx, id))
		list, err := repo.ListOverrides(t.Context(), tx, sched.ID)
		require.NoError(t, err)
		assert.Empty(t, list)
	})
}

func TestOnCallScheduleRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	alice := testutil.NewUser(t, tenantA, "analyst", nil)
	repo := repository.NewOnCallScheduleRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	sched := &domain.OnCallSchedule{
		TenantID:         tenantA,
		Name:             "Tenant A schedule",
		IsDefault:        true,
		HandoverAt:       time.Now(),
		PeriodDays:       7,
		ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
		Participants:     []domain.OnCallParticipant{{UserID: alice}},
	}
	require.NoError(t, repo.Insert(t.Context(), txA, sched))

	txB := testutil.BeginTx(t, pool, tenantB)
	t.Run("List doesn't see tenant A's schedules", func(t *testing.T) {
		list, err := repo.List(t.Context(), txB)
		require.NoError(t, err)
		assert.Empty(t, list)
	})
	t.Run("Get by id doesn't see tenant A's schedule", func(t *testing.T) {
		got, err := repo.Get(t.Context(), txB, sched.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
	t.Run("GetDefaultForResolution filters by tenantID explicitly -- not just is_default", func(t *testing.T) {
		// Regression coverage for the cross-tenant bug this exact query
		// class hit earlier today: under RLS this is already isolated, but
		// this method is also called through cmd/worker's BYPASSRLS
		// connection (see the method's doc comment), where RLS provides no
		// isolation at all and only the explicit tenant_id filter does.
		_, _, _, _, _, _, _, found, err := repo.GetDefaultForResolution(t.Context(), txB, tenantB, "2026-01-01")
		require.NoError(t, err)
		assert.False(t, found, "tenant B has no default schedule of its own, even though tenant A does")
	})
}
