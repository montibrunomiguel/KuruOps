import type { OnCallParticipant, OnCallWorkingHoursInterval, OnCallWorkingHoursMode } from "../types/onCallSchedule";

// Pure TS port of backend/internal/domain.ResolveOnCallSet -- same algorithm,
// used only by OnCallTimeline's live preview so the calendar renders without
// a network round-trip per keystroke while editing, and never drifts from
// what the backend will actually resolve. See the Go doc comment for the
// full algorithm explanation; kept in sync deliberately, not derived.
export function resolveOnCallSet(
  participants: OnCallParticipant[],
  handoverAt: Date,
  periodDays: number,
  concurrentShifts: number,
  mode: OnCallWorkingHoursMode,
  workingHours: OnCallWorkingHoursInterval[],
  override: OnCallParticipant | null,
  localNow: Date,
): OnCallParticipant[] {
  if (override) return [override];
  if (participants.length === 0 || localNow.getTime() < handoverAt.getTime()) return [];
  if (mode === "specific_times" && !withinWorkingHours(workingHours, localNow)) return [];

  const safePeriodDays = periodDays < 1 ? 1 : periodDays;
  const periodMs = safePeriodDays * 24 * 60 * 60 * 1000;
  const periodsElapsed = Math.floor((localNow.getTime() - handoverAt.getTime()) / periodMs);

  let groupSize = concurrentShifts < 1 ? 1 : concurrentShifts;
  if (groupSize > participants.length) groupSize = participants.length;
  const numGroups = Math.ceil(participants.length / groupSize);
  let groupIndex = periodsElapsed % numGroups;
  if (groupIndex < 0) groupIndex += numGroups;

  // Wraps around the end of the roster rather than truncating the last
  // group, so every period is fully staffed -- mirrors the Go original
  // exactly (see its comment for why, and for the back-to-back cost when
  // the roster does not divide evenly). Kept in step by the shared
  // fixtures in docs/oncall-rotation-fixtures.json.
  const out: OnCallParticipant[] = [];
  for (let i = 0; i < groupSize; i++) {
    out.push(participants[(groupIndex * groupSize + i) % participants.length]);
  }
  return out;
}

function withinWorkingHours(intervals: OnCallWorkingHoursInterval[], localNow: Date): boolean {
  const weekday = localNow.getDay();
  const minuteOfDay = localNow.getHours() * 60 + localNow.getMinutes();
  for (const iv of intervals) {
    if (!iv.weekdays.includes(weekday)) continue;
    if (iv.startMinute <= iv.endMinute) {
      if (minuteOfDay >= iv.startMinute && minuteOfDay <= iv.endMinute) return true;
    } else if (minuteOfDay >= iv.startMinute || minuteOfDay <= iv.endMinute) {
      return true;
    }
  }
  return false;
}
