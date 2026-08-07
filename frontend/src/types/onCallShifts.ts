// Mirrors backend/internal/domain.OnCallShift. startMinute/endMinute are
// minutes-since-midnight (0-1439) in the tenant's configured timezone, not a
// Postgres `time` string -- see the migration's comment for why.
export interface OnCallShift {
  id: string;
  tenantId: string;
  userId: string;
  userName: string;
  weekday: number;
  startMinute: number;
  endMinute: number;
  createdAt: string;
}

export interface OnCallSchedule {
  timezone: string;
  shifts: OnCallShift[];
}
