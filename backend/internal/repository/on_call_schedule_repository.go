package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

// OnCallScheduleRepository is the only place that writes SQL for
// on_call_schedules and its child tables (on_call_rotation_participants,
// on_call_working_hours, on_call_overrides). Any number of schedule rows per
// tenant, one of them is_default -- List/Get/Insert/Update/Delete operate on
// a specific schedule id, same shape as PlaybookRepository.
type OnCallScheduleRepository struct{}

func NewOnCallScheduleRepository() *OnCallScheduleRepository {
	return &OnCallScheduleRepository{}
}

const onCallScheduleColumns = `id, tenant_id, name, is_default, handover_at, period_days, concurrent_shifts, working_hours_mode, created_at, updated_at`

// List loads every schedule for the tenant (RLS-scoped, no explicit
// tenantID needed here -- unlike GetDefaultForResolution below, this is
// never called through cmd/worker's BYPASSRLS connection). Participants and
// working hours are loaded per row; overrides are omitted -- not relevant
// to a list view, and Get loads them for the one schedule being edited.
func (r *OnCallScheduleRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.OnCallSchedule, error) {
	rows, err := tx.Query(ctx, `select `+onCallScheduleColumns+` from on_call_schedules order by name asc`)
	if err != nil {
		return nil, fmt.Errorf("query on-call schedules: %w", err)
	}
	var schedules []domain.OnCallSchedule
	for rows.Next() {
		sched, err := scanOnCallScheduleRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		schedules = append(schedules, *sched)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate on-call schedules: %w", err)
	}

	for i := range schedules {
		participants, err := r.participantsFor(ctx, tx, schedules[i].ID)
		if err != nil {
			return nil, err
		}
		schedules[i].Participants = participants

		workingHours, err := r.workingHoursFor(ctx, tx, schedules[i].ID)
		if err != nil {
			return nil, err
		}
		schedules[i].WorkingHours = workingHours
	}
	return schedules, nil
}

// Get loads one schedule (by id, RLS scopes it to the current tenant) with
// its participants, working-hours intervals, and overrides -- nil, nil if
// no such schedule exists (or it belongs to another tenant, indistinguishable
// under RLS, same as every other by-id repository method in this codebase).
func (r *OnCallScheduleRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.OnCallSchedule, error) {
	row := tx.QueryRow(ctx, `select `+onCallScheduleColumns+` from on_call_schedules where id = $1`, id)
	sched, err := scanOnCallSchedule(row)
	if err != nil || sched == nil {
		return nil, err
	}

	participants, err := r.participantsFor(ctx, tx, sched.ID)
	if err != nil {
		return nil, err
	}
	sched.Participants = participants

	workingHours, err := r.workingHoursFor(ctx, tx, sched.ID)
	if err != nil {
		return nil, err
	}
	sched.WorkingHours = workingHours

	overrides, err := r.ListOverrides(ctx, tx, sched.ID)
	if err != nil {
		return nil, err
	}
	sched.Overrides = overrides

	return sched, nil
}

// Insert creates a new schedule row, then wholesale-replaces (from empty)
// its participants and working-hours rows -- same delete-then-reinsert
// idiom PlaybookRepository.replaceSteps uses.
func (r *OnCallScheduleRepository) Insert(ctx context.Context, tx pgx.Tx, sched *domain.OnCallSchedule) error {
	row := tx.QueryRow(ctx, `
		insert into on_call_schedules (tenant_id, name, is_default, handover_at, period_days, concurrent_shifts, working_hours_mode)
		values ($1,$2,$3,$4,$5,$6,$7)
		returning id, created_at, updated_at`,
		sched.TenantID, sched.Name, sched.IsDefault, sched.HandoverAt, sched.PeriodDays, sched.ConcurrentShifts, sched.WorkingHoursMode,
	)
	if err := row.Scan(&sched.ID, &sched.CreatedAt, &sched.UpdatedAt); err != nil {
		return fmt.Errorf("insert on-call schedule: %w", err)
	}
	if err := r.replaceParticipants(ctx, tx, sched.ID, sched.TenantID, sched.Participants); err != nil {
		return err
	}
	if err := r.replaceWorkingHours(ctx, tx, sched.ID, sched.TenantID, sched.WorkingHours); err != nil {
		return err
	}
	return nil
}

// Update saves an existing schedule's fields (IsDefault is never touched
// here -- see OnCallScheduleRepository.SetDefault, the only writer of that
// column after creation) and wholesale-replaces participants/working hours.
func (r *OnCallScheduleRepository) Update(ctx context.Context, tx pgx.Tx, sched *domain.OnCallSchedule) error {
	row := tx.QueryRow(ctx, `
		update on_call_schedules set
			name = $2, handover_at = $3, period_days = $4, concurrent_shifts = $5, working_hours_mode = $6, updated_at = now()
		where id = $1
		returning updated_at`,
		sched.ID, sched.Name, sched.HandoverAt, sched.PeriodDays, sched.ConcurrentShifts, sched.WorkingHoursMode,
	)
	if err := row.Scan(&sched.UpdatedAt); err != nil {
		return fmt.Errorf("update on-call schedule: %w", err)
	}
	if err := r.replaceParticipants(ctx, tx, sched.ID, sched.TenantID, sched.Participants); err != nil {
		return err
	}
	if err := r.replaceWorkingHours(ctx, tx, sched.ID, sched.TenantID, sched.WorkingHours); err != nil {
		return err
	}
	return nil
}

func (r *OnCallScheduleRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from on_call_schedules where id = $1`, id)
	return err
}

// SetDefault clears is_default on every schedule for tenantID, then sets it
// on id -- clear-then-set inside the caller's transaction, same pattern as
// LLMProviderRepository.SetDefault.
func (r *OnCallScheduleRepository) SetDefault(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `update on_call_schedules set is_default = false where tenant_id = $1`, tenantID); err != nil {
		return fmt.Errorf("clear default on-call schedule: %w", err)
	}
	if _, err := tx.Exec(ctx, `update on_call_schedules set is_default = true where id = $1`, id); err != nil {
		return fmt.Errorf("set default on-call schedule: %w", err)
	}
	return nil
}

func (r *OnCallScheduleRepository) replaceParticipants(ctx context.Context, tx pgx.Tx, scheduleID, tenantID uuid.UUID, participants []domain.OnCallParticipant) error {
	if _, err := tx.Exec(ctx, `delete from on_call_rotation_participants where schedule_id = $1`, scheduleID); err != nil {
		return fmt.Errorf("clear on-call participants: %w", err)
	}
	for i, p := range participants {
		if _, err := tx.Exec(ctx, `
			insert into on_call_rotation_participants (schedule_id, tenant_id, user_id, position)
			values ($1,$2,$3,$4)`,
			scheduleID, tenantID, p.UserID, i,
		); err != nil {
			return fmt.Errorf("insert on-call participant: %w", err)
		}
	}
	return nil
}

func (r *OnCallScheduleRepository) replaceWorkingHours(ctx context.Context, tx pgx.Tx, scheduleID, tenantID uuid.UUID, intervals []domain.OnCallWorkingHoursInterval) error {
	if _, err := tx.Exec(ctx, `delete from on_call_working_hours where schedule_id = $1`, scheduleID); err != nil {
		return fmt.Errorf("clear on-call working hours: %w", err)
	}
	for _, iv := range intervals {
		weekdays := make([]int32, len(iv.Weekdays))
		for i, w := range iv.Weekdays {
			weekdays[i] = int32(w)
		}
		if _, err := tx.Exec(ctx, `
			insert into on_call_working_hours (schedule_id, tenant_id, weekdays, start_minute, end_minute)
			values ($1,$2,$3,$4,$5)`,
			scheduleID, tenantID, weekdays, iv.StartMinute, iv.EndMinute,
		); err != nil {
			return fmt.Errorf("insert on-call working hours: %w", err)
		}
	}
	return nil
}

func (r *OnCallScheduleRepository) participantsFor(ctx context.Context, tx pgx.Tx, scheduleID uuid.UUID) ([]domain.OnCallParticipant, error) {
	rows, err := tx.Query(ctx, `
		select p.user_id, u.name from on_call_rotation_participants p
		join users u on u.id = p.user_id
		where p.schedule_id = $1
		order by p.position asc`,
		scheduleID,
	)
	if err != nil {
		return nil, fmt.Errorf("query on-call participants: %w", err)
	}
	defer rows.Close()

	participants := []domain.OnCallParticipant{}
	for rows.Next() {
		var p domain.OnCallParticipant
		if err := rows.Scan(&p.UserID, &p.UserName); err != nil {
			return nil, fmt.Errorf("scan on-call participant: %w", err)
		}
		participants = append(participants, p)
	}
	return participants, rows.Err()
}

func (r *OnCallScheduleRepository) workingHoursFor(ctx context.Context, tx pgx.Tx, scheduleID uuid.UUID) ([]domain.OnCallWorkingHoursInterval, error) {
	rows, err := tx.Query(ctx, `
		select id, weekdays, start_minute, end_minute from on_call_working_hours
		where schedule_id = $1
		order by start_minute asc`,
		scheduleID,
	)
	if err != nil {
		return nil, fmt.Errorf("query on-call working hours: %w", err)
	}
	defer rows.Close()

	intervals := []domain.OnCallWorkingHoursInterval{}
	for rows.Next() {
		var iv domain.OnCallWorkingHoursInterval
		var weekdays []int32
		if err := rows.Scan(&iv.ID, &weekdays, &iv.StartMinute, &iv.EndMinute); err != nil {
			return nil, fmt.Errorf("scan on-call working hours: %w", err)
		}
		iv.Weekdays = make([]int, len(weekdays))
		for i, w := range weekdays {
			iv.Weekdays[i] = int(w)
		}
		intervals = append(intervals, iv)
	}
	return intervals, rows.Err()
}

// GetDefaultForResolution is a lean load for the hot ResolveCurrentAnalyst
// path (consulted on every ingested alert, and every escalation sweep
// tick): just the default schedule's participants and rotation config, plus
// localDate's override if one exists -- skips the full override list Get()
// would otherwise load. found=false means no default schedule is configured
// for this tenant yet (including: tenant has schedules, but none marked
// default -- shouldn't happen given SetDefault's clear-then-set, but isn't
// treated as an error either way, same "gap is not an error" precedent).
//
// Filters explicitly by tenantID rather than leaning on RLS alone -- unlike
// most repositories here, this one is also queried through cmd/worker's
// BYPASSRLS connection, where relying on "is_default = true" alone would
// silently resolve against whichever tenant's default schedule happens to
// sort first instead of the one actually being resolved for.
func (r *OnCallScheduleRepository) GetDefaultForResolution(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, localDate string) (
	participants []domain.OnCallParticipant,
	handoverAt time.Time,
	periodDays, concurrentShifts int,
	mode domain.OnCallWorkingHoursMode,
	workingHours []domain.OnCallWorkingHoursInterval,
	override *domain.OnCallParticipant,
	found bool,
	err error,
) {
	var scheduleID uuid.UUID
	err = tx.QueryRow(ctx, `
		select id from on_call_schedules where tenant_id = $1 and is_default = true`,
		tenantID,
	).Scan(&scheduleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, time.Time{}, 0, 0, "", nil, nil, false, nil
		}
		return nil, time.Time{}, 0, 0, "", nil, nil, false, fmt.Errorf("get default on-call schedule for resolution: %w", err)
	}
	return r.resolveScheduleForID(ctx, tx, scheduleID, localDate)
}

// GetByIDForResolution is GetDefaultForResolution's counterpart for an
// escalation chain step, which resolves against whichever specific schedule
// the step references -- not necessarily the tenant's default. Same
// explicit-tenantID-filter reasoning as GetDefaultForResolution: also
// queried through cmd/worker's BYPASSRLS connection, so "id = $1" alone
// isn't enough to stop a step from resolving against another tenant's
// schedule if the ids ever collided across tenants (they can't, since
// scheduleID always comes from this tenant's own escalation_policy_steps
// row, but the explicit check costs nothing and matches the precedent).
func (r *OnCallScheduleRepository) GetByIDForResolution(ctx context.Context, tx pgx.Tx, tenantID, scheduleID uuid.UUID, localDate string) (
	participants []domain.OnCallParticipant,
	handoverAt time.Time,
	periodDays, concurrentShifts int,
	mode domain.OnCallWorkingHoursMode,
	workingHours []domain.OnCallWorkingHoursInterval,
	override *domain.OnCallParticipant,
	found bool,
	err error,
) {
	var exists bool
	err = tx.QueryRow(ctx, `select exists(select 1 from on_call_schedules where id = $1 and tenant_id = $2)`, scheduleID, tenantID).Scan(&exists)
	if err != nil {
		return nil, time.Time{}, 0, 0, "", nil, nil, false, fmt.Errorf("check on-call schedule tenant: %w", err)
	}
	if !exists {
		return nil, time.Time{}, 0, 0, "", nil, nil, false, nil
	}
	return r.resolveScheduleForID(ctx, tx, scheduleID, localDate)
}

// resolveScheduleForID is the lean load both GetDefaultForResolution and
// GetByIDForResolution share once they've settled on a scheduleID: just
// participants and rotation config, plus localDate's override if one
// exists -- skips the full override list Get() would otherwise load.
func (r *OnCallScheduleRepository) resolveScheduleForID(ctx context.Context, tx pgx.Tx, scheduleID uuid.UUID, localDate string) (
	participants []domain.OnCallParticipant,
	handoverAt time.Time,
	periodDays, concurrentShifts int,
	mode domain.OnCallWorkingHoursMode,
	workingHours []domain.OnCallWorkingHoursInterval,
	override *domain.OnCallParticipant,
	found bool,
	err error,
) {
	err = tx.QueryRow(ctx, `
		select handover_at, period_days, concurrent_shifts, working_hours_mode
		from on_call_schedules where id = $1`,
		scheduleID,
	).Scan(&handoverAt, &periodDays, &concurrentShifts, &mode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, time.Time{}, 0, 0, "", nil, nil, false, nil
		}
		return nil, time.Time{}, 0, 0, "", nil, nil, false, fmt.Errorf("get on-call schedule for resolution: %w", err)
	}

	participants, err = r.participantsFor(ctx, tx, scheduleID)
	if err != nil {
		return nil, time.Time{}, 0, 0, "", nil, nil, false, err
	}
	if mode == domain.OnCallWorkingHoursSpecificTimes {
		workingHours, err = r.workingHoursFor(ctx, tx, scheduleID)
		if err != nil {
			return nil, time.Time{}, 0, 0, "", nil, nil, false, err
		}
	}

	var ov domain.OnCallParticipant
	err = tx.QueryRow(ctx, `
		select o.user_id, u.name from on_call_overrides o
		join users u on u.id = o.user_id
		where o.schedule_id = $1 and o.override_date = $2`,
		scheduleID, localDate,
	).Scan(&ov.UserID, &ov.UserName)
	switch {
	case err == nil:
		override = &ov
	case errors.Is(err, pgx.ErrNoRows):
		override = nil
	default:
		return nil, time.Time{}, 0, 0, "", nil, nil, false, fmt.Errorf("get on-call override: %w", err)
	}

	return participants, handoverAt, periodDays, concurrentShifts, mode, workingHours, override, true, nil
}

// CreateOverride upserts the override for scheduleID+date -- creating a
// second override for the same date replaces the first (matches "click the
// day again to swap who's covering it" from the reference UI).
func (r *OnCallScheduleRepository) CreateOverride(ctx context.Context, tx pgx.Tx, tenantID, scheduleID, userID uuid.UUID, date string, createdBy uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		insert into on_call_overrides (schedule_id, tenant_id, user_id, override_date, created_by)
		values ($1,$2,$3,$4,$5)
		on conflict (schedule_id, override_date) do update set user_id = excluded.user_id, created_by = excluded.created_by
		returning id`,
		scheduleID, tenantID, userID, date, createdBy,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert on-call override: %w", err)
	}
	return id, nil
}

func (r *OnCallScheduleRepository) DeleteOverride(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from on_call_overrides where id = $1`, id)
	return err
}

func (r *OnCallScheduleRepository) ListOverrides(ctx context.Context, tx pgx.Tx, scheduleID uuid.UUID) ([]domain.OnCallOverride, error) {
	rows, err := tx.Query(ctx, `
		select o.id, o.override_date::text, o.user_id, u.name, o.created_at
		from on_call_overrides o
		join users u on u.id = o.user_id
		where o.schedule_id = $1
		order by o.override_date asc`,
		scheduleID,
	)
	if err != nil {
		return nil, fmt.Errorf("query on-call overrides: %w", err)
	}
	defer rows.Close()

	overrides := []domain.OnCallOverride{}
	for rows.Next() {
		var o domain.OnCallOverride
		if err := rows.Scan(&o.ID, &o.Date, &o.UserID, &o.UserName, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan on-call override: %w", err)
		}
		overrides = append(overrides, o)
	}
	return overrides, rows.Err()
}

func scanOnCallSchedule(row pgx.Row) (*domain.OnCallSchedule, error) {
	var s domain.OnCallSchedule
	err := row.Scan(&s.ID, &s.TenantID, &s.Name, &s.IsDefault, &s.HandoverAt, &s.PeriodDays, &s.ConcurrentShifts, &s.WorkingHoursMode, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan on-call schedule: %w", err)
	}
	return &s, nil
}

// scanOnCallScheduleRow is scanOnCallSchedule's pgx.Rows counterpart (List
// iterates rows, not a single QueryRow) -- same column order, no
// ErrNoRows case since Rows.Next() already gates that.
func scanOnCallScheduleRow(rows pgx.Rows) (*domain.OnCallSchedule, error) {
	var s domain.OnCallSchedule
	if err := rows.Scan(&s.ID, &s.TenantID, &s.Name, &s.IsDefault, &s.HandoverAt, &s.PeriodDays, &s.ConcurrentShifts, &s.WorkingHoursMode, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, fmt.Errorf("scan on-call schedule: %w", err)
	}
	return &s, nil
}
