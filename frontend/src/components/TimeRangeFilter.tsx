import { useTranslation } from "react-i18next";

export type TimeRangePreset = "" | "24h" | "7d" | "30d" | "90d" | "custom";

// from/to are <input type="datetime-local"> values (e.g. "2026-08-01T09:30"),
// only meaningful when preset === "custom" -- kept alongside preset (rather
// than as separate component state) so a single value/onChange pair covers
// both preset and custom-range selection, matching every other dashboard
// filter's controlled-input shape.
export interface TimeRangeValue {
  preset: TimeRangePreset;
  from: string;
  to: string;
}

export const EMPTY_TIME_RANGE: TimeRangeValue = { preset: "", from: "", to: "" };

const PRESET_HOURS: Record<Exclude<TimeRangePreset, "" | "custom">, number> = {
  "24h": 24,
  "7d": 24 * 7,
  "30d": 24 * 30,
  "90d": 24 * 90,
};

// Converts a TimeRangeValue into the {since, until} ISO timestamps the
// backend's dashboard endpoints expect (see
// backend/internal/httpserver/handlers/dashboard.go's parseSince/parseUntil)
// -- the "Any time" preset omits both entirely, a preset computes since from
// now (until stays open-ended, same as before), and "custom" reads the two
// datetime-local inputs. `new Date(datetimeLocalValue)` interprets that
// string in the browser's own timezone and converting toISOString() carries
// the offset correctly, so "09:00 local" reaches the backend as the right
// UTC instant regardless of what timezone the browser is in.
export function timeRangeParams(value: TimeRangeValue): { since?: string; until?: string } {
  if (value.preset === "custom") {
    return {
      since: value.from ? new Date(value.from).toISOString() : undefined,
      until: value.to ? new Date(value.to).toISOString() : undefined,
    };
  }
  if (value.preset === "") return {};
  return { since: new Date(Date.now() - PRESET_HOURS[value.preset] * 60 * 60 * 1000).toISOString() };
}

// Shared time-range control across the Dashboard's Alerts/Incidents/
// Follow-up tabs -- a preset dropdown for the common cases, plus a
// "Custom range" option that reveals two datetime-local inputs (native
// calendar + time-of-day picker, no date-picker library needed) for
// selecting an exact from/to window.
export function TimeRangeFilter({ value, onChange }: { value: TimeRangeValue; onChange: (v: TimeRangeValue) => void }) {
  const { t } = useTranslation();
  return (
    <>
      <select
        className="select"
        aria-label={t("dashboard.filters.timeRangeLabel") ?? undefined}
        value={value.preset}
        onChange={(e) => onChange({ ...value, preset: e.target.value as TimeRangePreset })}
      >
        <option value="">{t("dashboard.filters.anyTime")}</option>
        <option value="24h">{t("dashboard.filters.last24h")}</option>
        <option value="7d">{t("dashboard.filters.last7d")}</option>
        <option value="30d">{t("dashboard.filters.last30d")}</option>
        <option value="90d">{t("dashboard.filters.last90d")}</option>
        <option value="custom">{t("dashboard.filters.customRange")}</option>
      </select>
      {value.preset === "custom" && (
        <>
          <input
            type="datetime-local"
            className="input"
            aria-label={t("dashboard.filters.customRangeFrom") ?? undefined}
            value={value.from}
            onChange={(e) => onChange({ ...value, from: e.target.value })}
          />
          <input
            type="datetime-local"
            className="input"
            aria-label={t("dashboard.filters.customRangeTo") ?? undefined}
            value={value.to}
            onChange={(e) => onChange({ ...value, to: e.target.value })}
          />
        </>
      )}
    </>
  );
}
