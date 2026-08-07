package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type OnCallShiftRepository struct{}

func NewOnCallShiftRepository() *OnCallShiftRepository {
	return &OnCallShiftRepository{}
}

const onCallShiftColumns = `s.id, s.tenant_id, s.user_id, u.name, s.weekday, s.start_minute, s.end_minute, s.created_at`

const onCallShiftsFrom = `from on_call_shifts s join users u on u.id = s.user_id`

func (r *OnCallShiftRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.OnCallShift, error) {
	rows, err := tx.Query(ctx, `select `+onCallShiftColumns+` `+onCallShiftsFrom+` order by s.weekday asc, s.start_minute asc`)
	if err != nil {
		return nil, fmt.Errorf("query on-call shifts: %w", err)
	}
	defer rows.Close()

	shifts := []domain.OnCallShift{}
	for rows.Next() {
		s, err := scanOnCallShift(rows)
		if err != nil {
			return nil, err
		}
		shifts = append(shifts, *s)
	}
	return shifts, rows.Err()
}

func (r *OnCallShiftRepository) Create(ctx context.Context, tx pgx.Tx, s *domain.OnCallShift) error {
	row := tx.QueryRow(ctx, `
		insert into on_call_shifts (tenant_id, user_id, weekday, start_minute, end_minute)
		values ($1,$2,$3,$4,$5)
		returning id, created_at`,
		s.TenantID, s.UserID, s.Weekday, s.StartMinute, s.EndMinute,
	)
	if err := row.Scan(&s.ID, &s.CreatedAt); err != nil {
		return fmt.Errorf("insert on-call shift: %w", err)
	}
	return nil
}

func (r *OnCallShiftRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from on_call_shifts where id = $1`, id)
	return err
}

// ResolveCurrentAnalyst returns whoever is on shift right now, or nil if no
// shift covers this moment. weekday/prevWeekday/minuteOfDay are computed by
// the caller (OnCallShiftService.ResolveCurrentAnalyst) from "now" converted
// into the tenant's configured timezone -- this method does no time-zone
// math itself.
//
// Three cases, OR'd together:
//  1. A non-wrapping shift today (start <= end) whose range contains minuteOfDay.
//  2. A wrapping shift today (start > end, e.g. 22:00-06:00) where minuteOfDay
//     has already reached its start -- covers the evening half.
//  3. A wrapping shift that started yesterday and hasn't ended yet (minuteOfDay
//     is still before its end) -- covers the early-morning half that bled
//     past midnight.
//
// Overlapping shifts are allowed (see the migration's comment). When more
// than one analyst is on shift at the same moment, `order by random()`
// picks a uniformly random winner on each call -- each new alert lands on
// a different analyst roughly as often as any other, rather than always
// routing to whichever shift happened to sort first. This intentionally
// trades determinism for fairness: don't rely on the same overlapping
// on-call set resolving to the same analyst twice in a row.
func (r *OnCallShiftRepository) ResolveCurrentAnalyst(ctx context.Context, tx pgx.Tx, weekday, prevWeekday, minuteOfDay int) (*uuid.UUID, error) {
	var userID uuid.UUID
	err := tx.QueryRow(ctx, `
		select user_id from on_call_shifts
		where (
			weekday = $1 and (
				(start_minute <= end_minute and $3 between start_minute and end_minute)
				or (start_minute > end_minute and $3 >= start_minute)
			)
		) or (
			weekday = $2 and start_minute > end_minute and $3 <= end_minute
		)
		order by random()
		limit 1`,
		weekday, prevWeekday, minuteOfDay,
	).Scan(&userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve on-call analyst: %w", err)
	}
	return &userID, nil
}

func scanOnCallShift(row pgx.Row) (*domain.OnCallShift, error) {
	var s domain.OnCallShift
	err := row.Scan(&s.ID, &s.TenantID, &s.UserID, &s.UserName, &s.Weekday, &s.StartMinute, &s.EndMinute, &s.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan on-call shift: %w", err)
	}
	return &s, nil
}
