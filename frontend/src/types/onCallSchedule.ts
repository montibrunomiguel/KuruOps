// Mirrors backend/internal/domain.OnCallSchedule and its child shapes --
// any number of named rotations per tenant (ordered responders + cadence +
// concurrency + optional working hours + per-day overrides), one marked
// isDefault (the one alert auto-assignment resolves against), see
// backend/internal/domain/on_call_schedule.go.

export type OnCallWorkingHoursMode = "all_day" | "specific_times";

export interface OnCallParticipant {
  userId: string;
  userName: string;
}

export interface OnCallWorkingHoursInterval {
  id?: string;
  weekdays: number[]; // 0 (Sunday) - 6 (Saturday)
  startMinute: number; // minutes since midnight, 0-1439
  endMinute: number;
}

export interface OnCallOverride {
  id: string;
  date: string; // YYYY-MM-DD, in the tenant's timezone
  userId: string;
  userName: string;
  createdAt: string;
}

export interface OnCallSchedule {
  id: string;
  tenantId: string;
  name: string;
  isDefault: boolean;
  timezone: string;
  participants: OnCallParticipant[]; // ordered
  handoverAt: string;
  periodDays: number;
  concurrentShifts: number;
  workingHoursMode: OnCallWorkingHoursMode;
  workingHours: OnCallWorkingHoursInterval[];
  overrides: OnCallOverride[];
  createdAt: string;
  updatedAt: string;
}

// SaveOnCallScheduleRequest mirrors backend handlers'
// saveOnCallScheduleRequest -- the whole form is resubmitted on every save.
export interface SaveOnCallScheduleRequest {
  name: string;
  participantIds: string[]; // ordered
  handoverAt: string;
  periodDays: number;
  concurrentShifts: number;
  workingHoursMode: OnCallWorkingHoursMode;
  workingHours: OnCallWorkingHoursInterval[];
}
