package service_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestOnCallScheduleService_ListAutoCreatesDefault(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository())

	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	sched := list[0]
	assert.Equal(t, "Primary On-Call", sched.Name)
	assert.True(t, sched.IsDefault)
	assert.Equal(t, 7, sched.PeriodDays)
	assert.Equal(t, 1, sched.ConcurrentShifts)
	assert.Equal(t, domain.OnCallWorkingHoursAllDay, sched.WorkingHoursMode)
	assert.Equal(t, "UTC", sched.Timezone)
	assert.Empty(t, sched.Participants)

	t.Run("second call returns the same row, doesn't create another", func(t *testing.T) {
		again, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		require.Len(t, again, 1)
		assert.Equal(t, sched.ID, again[0].ID)
	})
}

func TestOnCallScheduleService_Create(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alice := testutil.NewUser(t, tenantID, "analyst", nil)
	svc := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository())

	validInput := func() domain.SaveOnCallScheduleInput {
		return domain.SaveOnCallScheduleInput{
			Name:             "Primary On-Call",
			ParticipantIDs:   []uuid.UUID{alice},
			HandoverAt:       time.Now(),
			PeriodDays:       7,
			ConcurrentShifts: 1,
			WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
		}
	}

	t.Run("rejects a blank name", func(t *testing.T) {
		in := validInput()
		in.Name = ""
		_, err := svc.Create(t.Context(), tenantID, in)
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("rejects period days less than 1", func(t *testing.T) {
		in := validInput()
		in.PeriodDays = 0
		_, err := svc.Create(t.Context(), tenantID, in)
		assert.ErrorContains(t, err, "period days")
	})

	t.Run("rejects concurrent shifts less than 1", func(t *testing.T) {
		in := validInput()
		in.ConcurrentShifts = 0
		_, err := svc.Create(t.Context(), tenantID, in)
		assert.ErrorContains(t, err, "concurrent shifts")
	})

	t.Run("rejects an invalid working hours mode", func(t *testing.T) {
		in := validInput()
		in.WorkingHoursMode = "bogus"
		_, err := svc.Create(t.Context(), tenantID, in)
		assert.ErrorContains(t, err, "invalid working hours mode")
	})

	t.Run("rejects an unknown participant id", func(t *testing.T) {
		in := validInput()
		in.ParticipantIDs = []uuid.UUID{uuid.New()}
		_, err := svc.Create(t.Context(), tenantID, in)
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("rejects a working hours interval with no weekdays", func(t *testing.T) {
		in := validInput()
		in.WorkingHoursMode = domain.OnCallWorkingHoursSpecificTimes
		in.WorkingHours = []domain.SaveWorkingHoursInput{{Weekdays: nil, StartMinute: 0, EndMinute: 60}}
		_, err := svc.Create(t.Context(), tenantID, in)
		assert.ErrorContains(t, err, "weekday")
	})

	t.Run("rejects an out-of-range weekday", func(t *testing.T) {
		in := validInput()
		in.WorkingHoursMode = domain.OnCallWorkingHoursSpecificTimes
		in.WorkingHours = []domain.SaveWorkingHoursInput{{Weekdays: []int{7}, StartMinute: 0, EndMinute: 60}}
		_, err := svc.Create(t.Context(), tenantID, in)
		assert.ErrorContains(t, err, "weekday")
	})

	t.Run("rejects an interval covering zero minutes", func(t *testing.T) {
		in := validInput()
		in.WorkingHoursMode = domain.OnCallWorkingHoursSpecificTimes
		in.WorkingHours = []domain.SaveWorkingHoursInput{{Weekdays: []int{1}, StartMinute: 60, EndMinute: 60}}
		_, err := svc.Create(t.Context(), tenantID, in)
		assert.ErrorContains(t, err, "zero minutes")
	})

	t.Run("the tenant's first schedule becomes the default automatically", func(t *testing.T) {
		sched, err := svc.Create(t.Context(), tenantID, validInput())
		require.NoError(t, err)
		require.Len(t, sched.Participants, 1)
		assert.Equal(t, alice, sched.Participants[0].UserID)
		assert.Equal(t, "UTC", sched.Timezone)
		assert.True(t, sched.IsDefault)
	})

	t.Run("a second schedule does not become the default", func(t *testing.T) {
		in := validInput()
		in.Name = "Secondary"
		sched, err := svc.Create(t.Context(), tenantID, in)
		require.NoError(t, err)
		assert.False(t, sched.IsDefault)
	})
}

func TestOnCallScheduleService_Update(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alice := testutil.NewUser(t, tenantID, "analyst", nil)
	svc := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository())

	created, err := svc.Create(t.Context(), tenantID, domain.SaveOnCallScheduleInput{
		Name: "Original", ParticipantIDs: []uuid.UUID{alice}, HandoverAt: time.Now(),
		PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	})
	require.NoError(t, err)
	require.True(t, created.IsDefault)

	t.Run("rejects a blank name", func(t *testing.T) {
		_, err := svc.Update(t.Context(), tenantID, created.ID, domain.SaveOnCallScheduleInput{
			Name: "", HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
		})
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("updates fields and preserves IsDefault", func(t *testing.T) {
		updated, err := svc.Update(t.Context(), tenantID, created.ID, domain.SaveOnCallScheduleInput{
			Name: "Renamed", HandoverAt: time.Now(), PeriodDays: 14, ConcurrentShifts: 2, WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
		})
		require.NoError(t, err)
		assert.Equal(t, "Renamed", updated.Name)
		assert.Equal(t, 14, updated.PeriodDays)
		assert.True(t, updated.IsDefault, "Update must not clear is_default")
	})
}

func TestOnCallScheduleService_Delete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository())

	newInput := func(name string) domain.SaveOnCallScheduleInput {
		return domain.SaveOnCallScheduleInput{Name: name, HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay}
	}

	def, err := svc.Create(t.Context(), tenantID, newInput("Default"))
	require.NoError(t, err)
	require.True(t, def.IsDefault)

	t.Run("blocks deleting the default schedule while another exists", func(t *testing.T) {
		other, err := svc.Create(t.Context(), tenantID, newInput("Other"))
		require.NoError(t, err)
		require.False(t, other.IsDefault)

		err = svc.Delete(t.Context(), tenantID, def.ID)
		assert.ErrorContains(t, err, "cannot delete the default schedule")

		t.Run("deleting the non-default one is fine", func(t *testing.T) {
			require.NoError(t, svc.Delete(t.Context(), tenantID, other.ID))
		})
	})

	t.Run("deleting the default schedule is allowed once it's the only one", func(t *testing.T) {
		require.NoError(t, svc.Delete(t.Context(), tenantID, def.ID))
		got, err := svc.Get(t.Context(), tenantID, def.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestOnCallScheduleService_SetDefault(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository())
	newInput := func(name string) domain.SaveOnCallScheduleInput {
		return domain.SaveOnCallScheduleInput{Name: name, HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay}
	}

	a, err := svc.Create(t.Context(), tenantID, newInput("A"))
	require.NoError(t, err)
	b, err := svc.Create(t.Context(), tenantID, newInput("B"))
	require.NoError(t, err)
	require.True(t, a.IsDefault)
	require.False(t, b.IsDefault)

	require.NoError(t, svc.SetDefault(t.Context(), tenantID, b.ID))

	gotA, err := svc.Get(t.Context(), tenantID, a.ID)
	require.NoError(t, err)
	gotB, err := svc.Get(t.Context(), tenantID, b.ID)
	require.NoError(t, err)
	assert.False(t, gotA.IsDefault)
	assert.True(t, gotB.IsDefault)
}

func TestOnCallScheduleService_Overrides(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actor := testutil.NewUser(t, tenantID, "admin", nil)
	alice := testutil.NewUser(t, tenantID, "analyst", nil)
	svc := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository())

	t.Run("fails when the referenced schedule doesn't exist", func(t *testing.T) {
		_, err := svc.CreateOverride(t.Context(), tenantID, uuid.New(), actor, alice, "2026-03-01")
		assert.ErrorContains(t, err, "schedule not found")
	})

	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	scheduleID := list[0].ID

	t.Run("rejects a malformed date", func(t *testing.T) {
		_, err := svc.CreateOverride(t.Context(), tenantID, scheduleID, actor, alice, "not-a-date")
		assert.ErrorContains(t, err, "invalid date")
	})

	t.Run("rejects an unknown user", func(t *testing.T) {
		_, err := svc.CreateOverride(t.Context(), tenantID, scheduleID, actor, uuid.New(), "2026-03-01")
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("creates and deletes an override", func(t *testing.T) {
		override, err := svc.CreateOverride(t.Context(), tenantID, scheduleID, actor, alice, "2026-03-01")
		require.NoError(t, err)
		assert.Equal(t, alice, override.UserID)
		assert.Equal(t, "2026-03-01", override.Date)

		require.NoError(t, svc.DeleteOverride(t.Context(), tenantID, override.ID))
	})
}

func TestOnCallScheduleService_ResolveCurrentAnalyst(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alice := testutil.NewUser(t, tenantID, "analyst", nil)
	svc := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository())

	t.Run("nil, nil when no schedule exists yet", func(t *testing.T) {
		got, err := svc.ResolveCurrentAnalyst(t.Context(), tenantID, time.Now())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	handover := time.Now().Add(-24 * time.Hour)
	defaultSched, err := svc.Create(t.Context(), tenantID, domain.SaveOnCallScheduleInput{
		Name:             "Primary On-Call",
		ParticipantIDs:   []uuid.UUID{alice},
		HandoverAt:       handover,
		PeriodDays:       7,
		ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	})
	require.NoError(t, err)
	require.True(t, defaultSched.IsDefault)

	t.Run("resolves the sole participant once the rotation has started", func(t *testing.T) {
		got, err := svc.ResolveCurrentAnalyst(t.Context(), tenantID, time.Now())
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, alice, *got)
	})

	t.Run("an override for today wins outright", func(t *testing.T) {
		bob := testutil.NewUser(t, tenantID, "analyst", nil)
		today := time.Now().Format("2006-01-02")
		_, err := svc.CreateOverride(t.Context(), tenantID, defaultSched.ID, alice, bob, today)
		require.NoError(t, err)

		got, err := svc.ResolveCurrentAnalyst(t.Context(), tenantID, time.Now())
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, bob, *got)
	})

	t.Run("a non-default schedule's participants are never resolved, even if it started long ago", func(t *testing.T) {
		carol := testutil.NewUser(t, tenantID, "analyst", nil)
		_, err := svc.Create(t.Context(), tenantID, domain.SaveOnCallScheduleInput{
			Name:             "Secondary",
			ParticipantIDs:   []uuid.UUID{carol},
			HandoverAt:       handover,
			PeriodDays:       7,
			ConcurrentShifts: 1,
			WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
		})
		require.NoError(t, err)

		got, err := svc.ResolveCurrentAnalyst(t.Context(), tenantID, time.Now())
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.NotEqual(t, carol, *got, "the secondary schedule must never be consulted for auto-assignment")
	})
}

func TestOnCallScheduleService_SetTimezone(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository())

	t.Run("rejects an unknown timezone", func(t *testing.T) {
		err := svc.SetTimezone(t.Context(), tenantID, "Nowhere/Fake")
		assert.ErrorContains(t, err, "unknown timezone")
	})

	t.Run("accepts a valid IANA timezone", func(t *testing.T) {
		require.NoError(t, svc.SetTimezone(t.Context(), tenantID, "America/Sao_Paulo"))
		tz, err := svc.GetTimezone(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, "America/Sao_Paulo", tz)
	})
}
