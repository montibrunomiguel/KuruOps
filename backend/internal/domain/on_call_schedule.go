package domain

import (
	"time"

	"github.com/google/uuid"
)

type OnCallWorkingHoursMode string

const (
	OnCallWorkingHoursAllDay        OnCallWorkingHoursMode = "all_day"
	OnCallWorkingHoursSpecificTimes OnCallWorkingHoursMode = "specific_times"
)

// OnCallParticipant is one member of the rotation, in order. UserName is
// denormalized via a join, same "cheap read, mutable source of truth is
// elsewhere" tradeoff as Alert.AssignedAnalystName.
type OnCallParticipant struct {
	UserID   uuid.UUID `json:"userId"`
	UserName string    `json:"userName"`
}

// OnCallWorkingHoursInterval restricts the rotation to specific weekdays and
// a time-of-day window -- only consulted when the schedule's
// WorkingHoursMode is OnCallWorkingHoursSpecificTimes. StartMinute/EndMinute
// are minutes-since-midnight (0-1439), same encoding choice as the old
// on_call_shifts table (sidesteps pgx's lack of a default `time` codec).
type OnCallWorkingHoursInterval struct {
	ID          uuid.UUID `json:"id"`
	Weekdays    []int     `json:"weekdays"`
	StartMinute int       `json:"startMinute"`
	EndMinute   int       `json:"endMinute"`
}

// OnCallOverride pins a single calendar date to a specific analyst,
// overriding whatever the rotation would otherwise compute for that whole
// day (see ResolveOnCallSet).
type OnCallOverride struct {
	ID        uuid.UUID `json:"id"`
	Date      string    `json:"date"` // YYYY-MM-DD, in the tenant's timezone
	UserID    uuid.UUID `json:"userId"`
	UserName  string    `json:"userName"`
	CreatedAt time.Time `json:"createdAt"`
}

// OnCallSchedule mirrors `on_call_schedules` + its child tables. Any number
// per tenant (e.g. one rotation per team) -- exactly one is marked
// IsDefault, which is the one OnCallScheduleService.ResolveCurrentAnalyst
// resolves against for automatic alert assignment and escalation. See
// OnCallScheduleService.List for the "always at least one to edit" default
// creation on a tenant's first visit.
type OnCallSchedule struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenantId"`
	Name      string    `json:"name"`
	IsDefault bool      `json:"isDefault"`
	// Timezone is read from tenants.timezone and denormalized onto every
	// response -- not a column on this table, see 0021_tenant_timezone.
	Timezone         string                       `json:"timezone"`
	Participants     []OnCallParticipant          `json:"participants"`
	HandoverAt       time.Time                    `json:"handoverAt"`
	PeriodDays       int                          `json:"periodDays"`
	ConcurrentShifts int                          `json:"concurrentShifts"`
	WorkingHoursMode OnCallWorkingHoursMode       `json:"workingHoursMode"`
	WorkingHours     []OnCallWorkingHoursInterval `json:"workingHours"`
	Overrides        []OnCallOverride             `json:"overrides"`
	CreatedAt        time.Time                    `json:"createdAt"`
	UpdatedAt        time.Time                    `json:"updatedAt"`
}

type SaveWorkingHoursInput struct {
	Weekdays    []int
	StartMinute int
	EndMinute   int
}

type SaveOnCallScheduleInput struct {
	Name             string
	ParticipantIDs   []uuid.UUID // ordered
	HandoverAt       time.Time
	PeriodDays       int
	ConcurrentShifts int
	WorkingHoursMode OnCallWorkingHoursMode
	WorkingHours     []SaveWorkingHoursInput
}

// ResolveOnCallSet returns whoever is on call at localNow (already converted
// to the tenant's configured timezone by the caller -- this function does no
// time-zone math itself). Returns nil when: the rotation hasn't started yet
// (localNow is before handoverAt), there are no participants, or
// workingHours restricts coverage and localNow falls outside every
// interval -- all three are legitimate coverage gaps, not errors, matching
// the old on_call_shifts model's "a gap in the schedule is not an error"
// precedent.
//
// An override for localNow's calendar date wins outright over the computed
// rotation, replacing the whole on-call set (not just one seat).
//
// Rotation math: participants are split into consecutive groups of
// concurrentShifts (the last group may be smaller if the count doesn't
// divide evenly); periodsElapsed = floor((localNow - handoverAt) /
// (periodDays * 24h)) selects which group is currently on call via
// periodsElapsed % numGroups. E.g. 4 participants, concurrency 2, weekly:
// period 0 = participants[0:2], period 1 = participants[2:4], period 2 =
// participants[0:2] again.
func ResolveOnCallSet(
	participants []OnCallParticipant,
	handoverAt time.Time,
	periodDays, concurrentShifts int,
	mode OnCallWorkingHoursMode,
	workingHours []OnCallWorkingHoursInterval,
	override *OnCallParticipant,
	localNow time.Time,
) []OnCallParticipant {
	if override != nil {
		return []OnCallParticipant{*override}
	}
	if len(participants) == 0 || localNow.Before(handoverAt) {
		return nil
	}
	if mode == OnCallWorkingHoursSpecificTimes && !withinWorkingHours(workingHours, localNow) {
		return nil
	}

	if periodDays < 1 {
		periodDays = 1
	}
	period := time.Duration(periodDays) * 24 * time.Hour
	periodsElapsed := int(localNow.Sub(handoverAt) / period)

	groupSize := concurrentShifts
	if groupSize < 1 {
		groupSize = 1
	}
	if groupSize > len(participants) {
		groupSize = len(participants)
	}
	numGroups := (len(participants) + groupSize - 1) / groupSize
	groupIndex := periodsElapsed % numGroups
	if groupIndex < 0 {
		groupIndex += numGroups
	}

	start := groupIndex * groupSize
	end := start + groupSize
	if end > len(participants) {
		end = len(participants)
	}
	return participants[start:end]
}

// withinWorkingHours does not carry a wrapping interval (e.g. 22:00-06:00)
// past midnight into the next weekday -- unlike the old on_call_shifts
// model, working hours here gate a secondary "business hours" restriction on
// top of the rotation, not the primary day-to-day handover itself, and every
// incident.io example of this field is same-day (e.g. 09:00-17:00 Mon-Fri).
// An admin covering a genuine overnight window should list both affected
// weekdays with a same-day-relative range instead.
func withinWorkingHours(intervals []OnCallWorkingHoursInterval, localNow time.Time) bool {
	weekday := int(localNow.Weekday())
	minuteOfDay := localNow.Hour()*60 + localNow.Minute()
	for _, iv := range intervals {
		if !containsWeekday(iv.Weekdays, weekday) {
			continue
		}
		if iv.StartMinute <= iv.EndMinute {
			if minuteOfDay >= iv.StartMinute && minuteOfDay <= iv.EndMinute {
				return true
			}
		} else {
			// Wrapping interval (e.g. 22:00-06:00).
			if minuteOfDay >= iv.StartMinute || minuteOfDay <= iv.EndMinute {
				return true
			}
		}
	}
	return false
}

func containsWeekday(weekdays []int, weekday int) bool {
	for _, w := range weekdays {
		if w == weekday {
			return true
		}
	}
	return false
}
