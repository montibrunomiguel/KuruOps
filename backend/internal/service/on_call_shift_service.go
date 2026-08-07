package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// OnCallShiftService owns Settings -> On-Call Schedule: weekly recurring
// shifts, the tenant's timezone they're interpreted in, and resolving
// "who's on call right now" for cmd/ingest's auto-assign step (see
// AlertService.EnableOnCallAutoAssign).
type OnCallShiftService struct {
	pool    *db.Pool
	repo    *repository.OnCallShiftRepository
	users   *repository.UserRepository
	tenants *repository.TenantRepository
}

func NewOnCallShiftService(pool *db.Pool, repo *repository.OnCallShiftRepository, users *repository.UserRepository, tenants *repository.TenantRepository) *OnCallShiftService {
	return &OnCallShiftService{pool: pool, repo: repo, users: users, tenants: tenants}
}

func (s *OnCallShiftService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.OnCallShift, error) {
	var shifts []domain.OnCallShift
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		shifts = v
		return err
	})
	return shifts, err
}

func (s *OnCallShiftService) Create(ctx context.Context, tenantID, userID uuid.UUID, weekday, startMinute, endMinute int) (*domain.OnCallShift, error) {
	if weekday < 0 || weekday > 6 {
		return nil, fmt.Errorf("weekday must be between 0 (Sunday) and 6 (Saturday)")
	}
	if startMinute < 0 || startMinute > 1439 || endMinute < 0 || endMinute > 1439 {
		return nil, fmt.Errorf("start/end minute must be between 0 and 1439")
	}
	if startMinute == endMinute {
		return nil, fmt.Errorf("a shift must cover more than zero minutes")
	}

	shift := &domain.OnCallShift{TenantID: tenantID, UserID: userID, Weekday: weekday, StartMinute: startMinute, EndMinute: endMinute}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.Get(ctx, tx, userID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("analyst %s not found", userID)
			}
			return fmt.Errorf("load analyst: %w", err)
		}
		if u == nil {
			return fmt.Errorf("analyst %s not found", userID)
		}
		shift.UserName = u.Name
		return s.repo.Create(ctx, tx, shift)
	})
	if err != nil {
		return nil, err
	}
	return shift, nil
}

func (s *OnCallShiftService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx, id)
	})
}

func (s *OnCallShiftService) GetTimezone(ctx context.Context, tenantID uuid.UUID) (string, error) {
	return s.tenants.GetTimezone(ctx, s.pool, tenantID)
}

func (s *OnCallShiftService) SetTimezone(ctx context.Context, tenantID uuid.UUID, timezone string) error {
	if _, err := time.LoadLocation(timezone); err != nil {
		return fmt.Errorf("unknown timezone %q: %w", timezone, err)
	}
	return s.tenants.SetTimezone(ctx, s.pool, tenantID, timezone)
}

// ResolveCurrentAnalyst converts now into the tenant's configured timezone
// and looks up whoever is on shift at that moment -- nil, nil means nobody
// is (a gap in the schedule is not an error). Used by AlertService.Ingest's
// auto-assign step when on-call auto-assignment is enabled.
func (s *OnCallShiftService) ResolveCurrentAnalyst(ctx context.Context, tenantID uuid.UUID, now time.Time) (*uuid.UUID, error) {
	tz, err := s.GetTimezone(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("load tenant timezone %q: %w", tz, err)
	}
	local := now.In(loc)
	weekday := int(local.Weekday())
	prevWeekday := (weekday + 6) % 7
	minuteOfDay := local.Hour()*60 + local.Minute()

	var analystID *uuid.UUID
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.ResolveCurrentAnalyst(ctx, tx, weekday, prevWeekday, minuteOfDay)
		analystID = v
		return err
	})
	return analystID, err
}
