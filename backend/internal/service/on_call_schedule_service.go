package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
)

// defaultScheduleName/defaultPeriodDays mirror the zero-config row
// OnCallScheduleService.List auto-creates the first time a tenant has no
// schedules at all -- weekly cadence, no participants, all day, matching
// what an empty rotation should sensibly default to before an admin
// configures it.
const (
	defaultScheduleName = "Primary On-Call"
	defaultPeriodDays   = 7
)

// onCallScheduleRepo is the subset of *repository.OnCallScheduleRepository
// this service calls -- an interface, not the concrete type, purely so
// tests can substitute a repo double that fails on demand to exercise the
// error-wrapping branches (a DB call failing mid-transaction) a real
// Postgres integration test can't trigger. *repository.OnCallScheduleRepository
// already satisfies this implicitly, so every existing constructor call
// site is unaffected.
type onCallScheduleRepo interface {
	List(ctx context.Context, tx pgx.Tx) ([]domain.OnCallSchedule, error)
	Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.OnCallSchedule, error)
	Insert(ctx context.Context, tx pgx.Tx, sched *domain.OnCallSchedule) error
	Update(ctx context.Context, tx pgx.Tx, sched *domain.OnCallSchedule) error
	Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
	SetDefault(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) error
	CreateOverride(ctx context.Context, tx pgx.Tx, tenantID, scheduleID, userID uuid.UUID, date string, createdBy uuid.UUID) (uuid.UUID, error)
	DeleteOverride(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
	GetDefaultForResolution(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, localDate string) ([]domain.OnCallParticipant, time.Time, int, int, domain.OnCallWorkingHoursMode, []domain.OnCallWorkingHoursInterval, *domain.OnCallParticipant, bool, error)
	GetByIDForResolution(ctx context.Context, tx pgx.Tx, tenantID, scheduleID uuid.UUID, localDate string) ([]domain.OnCallParticipant, time.Time, int, int, domain.OnCallWorkingHoursMode, []domain.OnCallWorkingHoursInterval, *domain.OnCallParticipant, bool, error)
}

// OnCallScheduleService owns Settings -> On-Call Schedule: any number of
// named rotations per tenant (ordered participants + cadence + concurrency +
// optional working hours + per-day overrides), exactly one of them marked
// default, the tenant's timezone they're interpreted in, and resolving
// "who's on call right now" against the default schedule for cmd/ingest's
// auto-assign step (AlertService.EnableOnCallAutoAssign) and cmd/worker's
// escalation sweep.
type OnCallScheduleService struct {
	pool    *db.Pool
	repo    onCallScheduleRepo
	users   *repository.UserRepository
	tenants *repository.TenantRepository
	audit   *repository.AdminAuditEventRepository
}

func NewOnCallScheduleService(pool *db.Pool, repo onCallScheduleRepo, users *repository.UserRepository, tenants *repository.TenantRepository, audit *repository.AdminAuditEventRepository) *OnCallScheduleService {
	return &OnCallScheduleService{pool: pool, repo: repo, users: users, tenants: tenants, audit: audit}
}

func onCallScheduleAuditFields(s *domain.OnCallSchedule) map[string]any {
	participantIDs := make([]uuid.UUID, len(s.Participants))
	for i, p := range s.Participants {
		participantIDs[i] = p.UserID
	}
	return map[string]any{
		"name": s.Name, "handoverAt": s.HandoverAt, "periodDays": s.PeriodDays,
		"concurrentShifts": s.ConcurrentShifts, "workingHoursMode": s.WorkingHoursMode,
		"participantIds": participantIDs, "isDefault": s.IsDefault,
	}
}

// List returns every schedule for the tenant, auto-creating one empty
// default schedule if the tenant has none yet -- so the Settings list page
// always has at least one row to click into, mirroring the old single-
// schedule model's "one implicit schedule per tenant" behavior.
func (s *OnCallScheduleService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.OnCallSchedule, error) {
	tz, err := s.GetTimezone(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	var schedules []domain.OnCallSchedule
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		if err != nil {
			return err
		}
		if len(v) == 0 {
			sched := &domain.OnCallSchedule{
				TenantID:         tenantID,
				Name:             defaultScheduleName,
				IsDefault:        true,
				HandoverAt:       time.Now(),
				PeriodDays:       defaultPeriodDays,
				ConcurrentShifts: 1,
				WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
				Participants:     []domain.OnCallParticipant{},
				WorkingHours:     []domain.OnCallWorkingHoursInterval{},
				Overrides:        []domain.OnCallOverride{},
			}
			if err := s.repo.Insert(ctx, tx, sched); err != nil {
				return err
			}
			v = []domain.OnCallSchedule{*sched}
		}
		schedules = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range schedules {
		schedules[i].Timezone = tz
	}
	return schedules, nil
}

// Get returns one schedule by id, nil,nil if it doesn't exist (or belongs
// to another tenant, indistinguishable under RLS).
func (s *OnCallScheduleService) Get(ctx context.Context, tenantID, id uuid.UUID) (*domain.OnCallSchedule, error) {
	tz, err := s.GetTimezone(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var sched *domain.OnCallSchedule
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.Get(ctx, tx, id)
		if err != nil {
			return err
		}
		sched = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	if sched == nil {
		return nil, nil
	}
	sched.Timezone = tz
	return sched, nil
}

func validateScheduleInput(in domain.SaveOnCallScheduleInput) error {
	if in.Name == "" {
		return fmt.Errorf("schedule name is required")
	}
	if in.PeriodDays < 1 {
		return fmt.Errorf("period days must be at least 1")
	}
	if in.ConcurrentShifts < 1 {
		return fmt.Errorf("concurrent shifts must be at least 1")
	}
	if in.WorkingHoursMode != domain.OnCallWorkingHoursAllDay && in.WorkingHoursMode != domain.OnCallWorkingHoursSpecificTimes {
		return fmt.Errorf("invalid working hours mode %q", in.WorkingHoursMode)
	}
	for _, iv := range in.WorkingHours {
		if len(iv.Weekdays) == 0 {
			return fmt.Errorf("a working hours interval needs at least one weekday")
		}
		for _, w := range iv.Weekdays {
			if w < 0 || w > 6 {
				return fmt.Errorf("weekday must be between 0 (Sunday) and 6 (Saturday)")
			}
		}
		if iv.StartMinute < 0 || iv.StartMinute > 1439 || iv.EndMinute < 0 || iv.EndMinute > 1439 {
			return fmt.Errorf("start/end minute must be between 0 and 1439")
		}
		if iv.StartMinute == iv.EndMinute {
			return fmt.Errorf("a working hours interval must cover more than zero minutes")
		}
	}
	return nil
}

func buildScheduleFromInput(tenantID uuid.UUID, in domain.SaveOnCallScheduleInput) *domain.OnCallSchedule {
	sched := &domain.OnCallSchedule{
		TenantID:         tenantID,
		Name:             in.Name,
		HandoverAt:       in.HandoverAt,
		PeriodDays:       in.PeriodDays,
		ConcurrentShifts: in.ConcurrentShifts,
		WorkingHoursMode: in.WorkingHoursMode,
		Participants:     []domain.OnCallParticipant{},
		WorkingHours:     []domain.OnCallWorkingHoursInterval{},
		Overrides:        []domain.OnCallOverride{},
	}
	for _, iv := range in.WorkingHours {
		sched.WorkingHours = append(sched.WorkingHours, domain.OnCallWorkingHoursInterval{
			Weekdays: iv.Weekdays, StartMinute: iv.StartMinute, EndMinute: iv.EndMinute,
		})
	}
	return sched
}

func (s *OnCallScheduleService) resolveParticipants(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, sched *domain.OnCallSchedule, participantIDs []uuid.UUID) error {
	for _, userID := range participantIDs {
		u, err := s.users.Get(ctx, tx, tenantID, userID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("analyst %s not found", userID)
			}
			return fmt.Errorf("load analyst: %w", err)
		}
		if u == nil {
			return fmt.Errorf("analyst %s not found", userID)
		}
		sched.Participants = append(sched.Participants, domain.OnCallParticipant{UserID: u.ID, UserName: u.Name})
	}
	return nil
}

// Create validates and inserts a new schedule. The tenant's very first
// schedule is automatically marked default (so there's always exactly one
// once any schedule exists); later ones start out non-default -- promote
// via SetDefault.
func (s *OnCallScheduleService) Create(ctx context.Context, tenantID, actorID uuid.UUID, in domain.SaveOnCallScheduleInput) (*domain.OnCallSchedule, error) {
	if err := validateScheduleInput(in); err != nil {
		return nil, err
	}
	sched := buildScheduleFromInput(tenantID, in)

	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.resolveParticipants(ctx, tx, tenantID, sched, in.ParticipantIDs); err != nil {
			return err
		}
		existing, err := s.repo.List(ctx, tx)
		if err != nil {
			return fmt.Errorf("check existing schedules: %w", err)
		}
		sched.IsDefault = len(existing) == 0
		if err := s.repo.Insert(ctx, tx, sched); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": nil, "to": onCallScheduleAuditFields(sched)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "on-call-schedule", Action: "create", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, err
	}

	tz, err := s.GetTimezone(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	sched.Timezone = tz
	return sched, nil
}

// Update validates and saves an existing schedule's fields -- IsDefault is
// left untouched (see SetDefault).
func (s *OnCallScheduleService) Update(ctx context.Context, tenantID, actorID, id uuid.UUID, in domain.SaveOnCallScheduleInput) (*domain.OnCallSchedule, error) {
	if err := validateScheduleInput(in); err != nil {
		return nil, err
	}
	sched := buildScheduleFromInput(tenantID, in)
	sched.ID = id

	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.Get(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("load schedule: %w", err)
		}
		if existing == nil {
			return fmt.Errorf("schedule not found")
		}
		sched.IsDefault = existing.IsDefault

		if err := s.resolveParticipants(ctx, tx, tenantID, sched, in.ParticipantIDs); err != nil {
			return err
		}
		if err := s.repo.Update(ctx, tx, sched); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": onCallScheduleAuditFields(existing), "to": onCallScheduleAuditFields(sched)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "on-call-schedule", Action: "update", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, err
	}

	tz, err := s.GetTimezone(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	sched.Timezone = tz
	return sched, nil
}

// Delete removes a schedule. Deleting the tenant's default schedule is
// blocked while other schedules still exist -- there must always be an
// unambiguous default for ResolveCurrentAnalyst to resolve against as long
// as any schedule remains. Deleting the tenant's only schedule (default or
// not) is allowed; the tenant just goes back to "no schedule configured",
// already a legitimate gap, not an error, elsewhere in this service.
func (s *OnCallScheduleService) Delete(ctx context.Context, tenantID, actorID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		sched, err := s.repo.Get(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("load schedule: %w", err)
		}
		if sched == nil {
			return fmt.Errorf("schedule not found")
		}
		if sched.IsDefault {
			others, err := s.repo.List(ctx, tx)
			if err != nil {
				return fmt.Errorf("check other schedules: %w", err)
			}
			if len(others) > 1 {
				return fmt.Errorf("cannot delete the default schedule while other schedules exist -- set another as default first")
			}
		}
		if err := s.repo.Delete(ctx, tx, id); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": onCallScheduleAuditFields(sched), "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "on-call-schedule", Action: "delete", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// SetDefault promotes id to be tenantID's default schedule (and, via
// OnCallScheduleRepository.SetDefault's clear-then-set, demotes whichever
// schedule held that role before).
func (s *OnCallScheduleService) SetDefault(ctx context.Context, tenantID, actorID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.SetDefault(ctx, tx, tenantID, id); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"to": map[string]any{"defaultScheduleId": id}})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "on-call-schedule", Action: "set-default", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// CreateOverride assigns userID as the sole on-call analyst for date
// (YYYY-MM-DD, in the tenant's timezone) on the given schedule, replacing
// whatever the rotation would otherwise compute for that whole day.
func (s *OnCallScheduleService) CreateOverride(ctx context.Context, tenantID, scheduleID, actorID, userID uuid.UUID, date string) (*domain.OnCallOverride, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, fmt.Errorf("invalid date %q: expected YYYY-MM-DD", date)
	}

	var result domain.OnCallOverride
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.Get(ctx, tx, tenantID, userID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("analyst %s not found", userID)
			}
			return fmt.Errorf("load analyst: %w", err)
		}
		if u == nil {
			return fmt.Errorf("analyst %s not found", userID)
		}

		sched, err := s.repo.Get(ctx, tx, scheduleID)
		if err != nil {
			return fmt.Errorf("load schedule: %w", err)
		}
		if sched == nil {
			return fmt.Errorf("schedule not found")
		}

		id, err := s.repo.CreateOverride(ctx, tx, tenantID, sched.ID, userID, date, actorID)
		if err != nil {
			return err
		}
		result = domain.OnCallOverride{ID: id, Date: date, UserID: u.ID, UserName: u.Name}
		data, _ := json.Marshal(map[string]any{"to": map[string]any{
			"scheduleId": sched.ID, "date": date, "userId": u.ID, "userName": u.Name,
		}})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "on-call-schedule", Action: "create-override", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *OnCallScheduleService) DeleteOverride(ctx context.Context, tenantID, actorID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.DeleteOverride(ctx, tx, id); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": map[string]any{"id": id}, "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "on-call-schedule", Action: "delete-override", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

func (s *OnCallScheduleService) GetTimezone(ctx context.Context, tenantID uuid.UUID) (string, error) {
	return s.tenants.GetTimezone(ctx, s.pool, tenantID)
}

func (s *OnCallScheduleService) SetTimezone(ctx context.Context, tenantID, actorID uuid.UUID, timezone string) error {
	if _, err := time.LoadLocation(timezone); err != nil {
		return fmt.Errorf("unknown timezone %q: %w", timezone, err)
	}
	before, err := s.tenants.GetTimezone(ctx, s.pool, tenantID)
	if err != nil {
		return err
	}
	if err := s.tenants.SetTimezone(ctx, s.pool, tenantID, timezone); err != nil {
		return err
	}
	// tenants isn't RLS-scoped (it's the tenant row itself), so SetTimezone
	// above goes straight through s.pool rather than s.pool.WithTenant --
	// this separate WithTenant call exists purely to get a tx for the audit
	// insert, same convention as every other admin_audit_events write.
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		data, _ := json.Marshal(map[string]any{
			"from": map[string]any{"timezone": before}, "to": map[string]any{"timezone": timezone},
		})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "on-call-schedule", Action: "set-timezone", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// ResolveCurrentAnalyst converts now into the tenant's configured timezone
// and resolves who's on call at that moment on the tenant's DEFAULT
// schedule via domain.ResolveOnCallSet -- nil, nil means nobody is (a gap
// in the schedule, or no default schedule configured yet, is not an
// error). When more than one analyst is on call at once (ConcurrentShifts >
// 1), picks one uniformly at random -- same fairness reasoning the old
// on_call_shifts model applied to overlapping shifts, so alert volume
// splits roughly evenly across everyone covering the same slot rather than
// always landing on the first one. Used by AlertService.Ingest's auto-
// assign step and cmd/worker's escalation sweep/notification.
func (s *OnCallScheduleService) ResolveCurrentAnalyst(ctx context.Context, tenantID uuid.UUID, now time.Time) (*uuid.UUID, error) {
	return s.resolveAnalyst(ctx, tenantID, now, func(tx pgx.Tx, localDate string) ([]domain.OnCallParticipant, time.Time, int, int, domain.OnCallWorkingHoursMode, []domain.OnCallWorkingHoursInterval, *domain.OnCallParticipant, bool, error) {
		return s.repo.GetDefaultForResolution(ctx, tx, tenantID, localDate)
	})
}

// ResolveAnalystForSchedule is ResolveCurrentAnalyst's counterpart for an
// escalation chain step, which resolves against whichever specific
// scheduleID the step references -- not necessarily the tenant's default.
// Used by EscalationPolicyService.ResolveStepNotification.
func (s *OnCallScheduleService) ResolveAnalystForSchedule(ctx context.Context, tenantID, scheduleID uuid.UUID, now time.Time) (*uuid.UUID, error) {
	return s.resolveAnalyst(ctx, tenantID, now, func(tx pgx.Tx, localDate string) ([]domain.OnCallParticipant, time.Time, int, int, domain.OnCallWorkingHoursMode, []domain.OnCallWorkingHoursInterval, *domain.OnCallParticipant, bool, error) {
		return s.repo.GetByIDForResolution(ctx, tx, tenantID, scheduleID, localDate)
	})
}

// CurrentOnCallEntry is one schedule's answer to "who is on call right now".
type CurrentOnCallEntry struct {
	ScheduleID   uuid.UUID                  `json:"scheduleId"`
	ScheduleName string                     `json:"scheduleName"`
	IsDefault    bool                       `json:"isDefault"`
	OnCall       []domain.OnCallParticipant `json:"onCall"`
}

// CurrentOnCall answers "who is on call right now", for every schedule the
// tenant has, in the tenant's own timezone.
//
// This existed nowhere before: on-call resolution only ever happened inside
// escalation delivery, where it picks ONE analyst at random to notify, so
// there was no way for anyone -- an analyst, a status page, a paging
// integration -- to simply ask. The Settings timeline had to re-implement
// the rotation in TypeScript to draw its calendar, which is why
// docs/oncall-rotation-fixtures.json exists to stop the two copies drifting.
//
// Returns the full set rather than a single pick: with ConcurrentShifts > 1
// there genuinely are several people on call, and collapsing that to one is
// a delivery decision, not a fact about the schedule.
func (s *OnCallScheduleService) CurrentOnCall(ctx context.Context, tenantID uuid.UUID, now time.Time) ([]CurrentOnCallEntry, error) {
	tz, err := s.GetTimezone(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("load tenant timezone %q: %w", tz, err)
	}
	local := now.In(loc)
	localDate := local.Format("2006-01-02")

	schedules, err := s.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	entries := make([]CurrentOnCallEntry, 0, len(schedules))
	for _, sched := range schedules {
		var onCall []domain.OnCallParticipant
		err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
			participants, handoverAt, periodDays, concurrentShifts, mode, workingHours, override, found, err := s.repo.GetByIDForResolution(ctx, tx, tenantID, sched.ID, localDate)
			if err != nil || !found {
				return err
			}
			onCall = domain.ResolveOnCallSet(participants, handoverAt.In(loc), periodDays, concurrentShifts, mode, workingHours, override, local)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("resolve schedule %s: %w", sched.ID, err)
		}
		// An empty set is a real answer -- a working-hours gap, or a
		// schedule with nobody on it -- so the entry is kept rather than
		// dropped. "Nobody is on call" is exactly what a caller needs to
		// be able to see.
		entries = append(entries, CurrentOnCallEntry{
			ScheduleID: sched.ID, ScheduleName: sched.Name,
			IsDefault: sched.IsDefault, OnCall: orEmptyParticipants(onCall),
		})
	}
	return entries, nil
}

// orEmptyParticipants keeps the JSON field an empty array rather than null,
// so a client can iterate it without a nil check.
func orEmptyParticipants(in []domain.OnCallParticipant) []domain.OnCallParticipant {
	if in == nil {
		return []domain.OnCallParticipant{}
	}
	return in
}

// resolveAnalyst is ResolveCurrentAnalyst/ResolveAnalystForSchedule's shared
// implementation -- converts now into the tenant's configured timezone and
// resolves who's on call at that moment via domain.ResolveOnCallSet,
// against whichever schedule load returns (the default schedule, or a
// specific one). nil, nil means nobody is (a schedule gap, or no schedule
// found, is not an error). When more than one analyst is on call at once
// (ConcurrentShifts > 1), picks one uniformly at random -- same fairness
// reasoning the old on_call_shifts model applied to overlapping shifts, so
// alert volume splits roughly evenly across everyone covering the same slot
// rather than always landing on the first one.
func (s *OnCallScheduleService) resolveAnalyst(
	ctx context.Context, tenantID uuid.UUID, now time.Time,
	load func(tx pgx.Tx, localDate string) ([]domain.OnCallParticipant, time.Time, int, int, domain.OnCallWorkingHoursMode, []domain.OnCallWorkingHoursInterval, *domain.OnCallParticipant, bool, error),
) (*uuid.UUID, error) {
	tz, err := s.GetTimezone(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("load tenant timezone %q: %w", tz, err)
	}
	local := now.In(loc)
	localDate := local.Format("2006-01-02")

	var onCall []domain.OnCallParticipant
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		participants, handoverAt, periodDays, concurrentShifts, mode, workingHours, override, found, err := load(tx, localDate)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		onCall = domain.ResolveOnCallSet(participants, handoverAt.In(loc), periodDays, concurrentShifts, mode, workingHours, override, local)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(onCall) == 0 {
		return nil, nil
	}
	picked := onCall[rand.Intn(len(onCall))].UserID
	return &picked, nil
}
