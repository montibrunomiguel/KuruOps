import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";
import type { OnCallParticipant, OnCallSchedule } from "../types/onCallSchedule";
import type { UserSummary } from "../types/users";
import { resolveOnCallSet } from "../lib/onCallRotation";
import { personColor } from "../lib/personColor";

const ZOOM_OPTIONS = [1, 2, 4] as const;
type ZoomWeeks = (typeof ZOOM_OPTIONS)[number];

function startOfWeek(d: Date): Date {
  const copy = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  copy.setDate(copy.getDate() - copy.getDay());
  return copy;
}

function addDays(d: Date, n: number): Date {
  const copy = new Date(d);
  copy.setDate(copy.getDate() + n);
  return copy;
}

function isoDate(d: Date): string {
  const y = d.getFullYear();
  const m = (d.getMonth() + 1).toString().padStart(2, "0");
  const day = d.getDate().toString().padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

interface Run {
  participant: OnCallParticipant;
  startCol: number; // 1-indexed, inclusive
  endCol: number; // 1-indexed, exclusive
}

// OnCallTimeline is the live calendar preview under Settings -> On-Call
// Schedules -> a schedule's editor: a day-column grid, one row per
// concurrent slot, colored per person via lib/personColor, computed
// client-side with lib/onCallRotation (a pure port of the backend's
// domain.ResolveOnCallSet) so it renders with no network round-trip while
// the form above is being edited. Clicking a day opens a small popup to pin
// an override for that date -- see OnCallScheduleDetailPage for where
// `schedule`/`directory` come from and how `onOverrideChange` triggers a
// reload after a mutation. Override reads/writes are scoped to
// `schedule.id` -- a schedule can only have overrides once it's been
// created (this component is never rendered for the "new" unsaved draft).
export function OnCallTimeline({
  schedule,
  directory,
  onOverrideChange,
}: {
  schedule: OnCallSchedule;
  directory: UserSummary[];
  onOverrideChange: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [anchor, setAnchor] = useState<Date>(() => startOfWeek(new Date()));
  const [zoomWeeks, setZoomWeeks] = useState<ZoomWeeks>(1);
  const [popoverDate, setPopoverDate] = useState<string | null>(null);

  const dayCount = zoomWeeks * 7;
  const days = useMemo(() => Array.from({ length: dayCount }, (_, i) => addDays(anchor, i)), [anchor, dayCount]);

  const handoverAt = useMemo(() => new Date(schedule.handoverAt), [schedule.handoverAt]);

  // dayParticipants[i] is who's on call (in slot order) for days[i] -- an
  // override for that date wins outright, same rule as the backend.
  const dayParticipants = useMemo(() => {
    return days.map((day) => {
      const dateStr = isoDate(day);
      const override = schedule.overrides.find((o) => o.date === dateStr);
      if (override) return [{ userId: override.userId, userName: override.userName }];
      const noon = new Date(day.getFullYear(), day.getMonth(), day.getDate(), 12, 0, 0);
      return resolveOnCallSet(
        schedule.participants,
        handoverAt,
        schedule.periodDays,
        schedule.concurrentShifts,
        schedule.workingHoursMode,
        schedule.workingHours,
        null,
        noon,
      );
    });
  }, [days, schedule, handoverAt]);

  const slotCount = Math.max(1, schedule.concurrentShifts);
  const slotRuns: Run[][] = useMemo(() => {
    const rows: Run[][] = [];
    for (let slot = 0; slot < slotCount; slot++) {
      const runs: Run[] = [];
      let current: Run | null = null;
      for (let i = 0; i < days.length; i++) {
        const p = dayParticipants[i][slot];
        if (p && current && current.participant.userId === p.userId) {
          current.endCol = i + 2;
        } else {
          if (current) runs.push(current);
          current = p ? { participant: p, startCol: i + 1, endCol: i + 2 } : null;
        }
      }
      if (current) runs.push(current);
      rows.push(runs);
    }
    return rows;
  }, [days, dayParticipants, slotCount]);

  const today = new Date();
  const todayIndex = days.findIndex((d) => sameDay(d, today));
  const nowLeftPercent =
    todayIndex >= 0 ? ((todayIndex + (today.getHours() * 60 + today.getMinutes()) / 1440) / dayCount) * 100 : null;

  const gridTemplateColumns = `repeat(${dayCount}, minmax(28px, 1fr))`;

  const popoverOverride = popoverDate ? schedule.overrides.find((o) => o.date === popoverDate) ?? null : null;

  return (
    <div className="panel" style={{ marginTop: 16 }}>
      <div className="panel-header">
        <h3 className="panel-title" style={{ fontSize: 14 }}>
          {t("settings.onCallSchedule.timeline.title")}
        </h3>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <button className="btn btn-sm" onClick={() => setAnchor(startOfWeek(new Date()))}>
            {t("settings.onCallSchedule.timeline.today")}
          </button>
          <button
            className="btn btn-ghost btn-sm"
            aria-label={t("settings.onCallSchedule.timeline.previous")}
            onClick={() => setAnchor((a) => addDays(a, -dayCount))}
          >
            ←
          </button>
          <button
            className="btn btn-ghost btn-sm"
            aria-label={t("settings.onCallSchedule.timeline.next")}
            onClick={() => setAnchor((a) => addDays(a, dayCount))}
          >
            →
          </button>
          <select
            className="select"
            aria-label={t("settings.onCallSchedule.timeline.zoom")}
            value={zoomWeeks}
            onChange={(e) => setZoomWeeks(Number(e.target.value) as ZoomWeeks)}
          >
            {ZOOM_OPTIONS.map((w) => (
              <option key={w} value={w}>
                {t("settings.onCallSchedule.timeline.zoomWeeks", { count: w })}
              </option>
            ))}
          </select>
        </div>
      </div>

      {schedule.participants.length === 0 && (
        <div className="empty-state">{t("settings.onCallSchedule.timeline.noResponders")}</div>
      )}

      <div style={{ position: "relative", overflowX: "auto" }}>
        <div style={{ display: "grid", gridTemplateColumns, gap: 2, marginBottom: 4 }}>
          {days.map((d) => (
            <button
              key={isoDate(d)}
              type="button"
              className="btn btn-ghost btn-sm"
              style={{
                fontSize: 11,
                padding: "2px 0",
                fontWeight: sameDay(d, today) ? 700 : 400,
              }}
              onClick={() => setPopoverDate(isoDate(d))}
              title={t("settings.onCallSchedule.override.create")}
            >
              {d.getDate()}
            </button>
          ))}
        </div>

        <div style={{ position: "relative" }}>
          {nowLeftPercent !== null && (
            <div
              data-testid="oncall-now-line"
              style={{
                position: "absolute",
                top: 0,
                bottom: 0,
                left: `${nowLeftPercent}%`,
                width: 2,
                background: "var(--critical)",
                zIndex: 1,
              }}
            />
          )}
          {slotRuns.map((runs, slot) => (
            <div key={slot} style={{ display: "grid", gridTemplateColumns, gap: 2, marginBottom: 4, minHeight: 22 }}>
              {runs.map((run, idx) => (
                <div
                  key={idx}
                  style={{
                    gridColumn: `${run.startCol} / ${run.endCol}`,
                    background: personColor(run.participant.userId),
                    color: "#fff",
                    borderRadius: 4,
                    fontSize: 11,
                    padding: "2px 6px",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                  title={run.participant.userName}
                >
                  {run.participant.userName}
                </div>
              ))}
            </div>
          ))}
        </div>
      </div>

      {popoverDate && (
        <div className="modal-overlay" onClick={() => setPopoverDate(null)}>
          <div className="modal" style={{ maxWidth: 360 }} onClick={(e) => e.stopPropagation()}>
            <h3 className="modal-title">
              {t("settings.onCallSchedule.override.title", { date: popoverDate })}
            </h3>
            <OverridePopoverForm
              scheduleId={schedule.id}
              date={popoverDate}
              existing={popoverOverride}
              directory={directory}
              token={token}
              onClose={() => setPopoverDate(null)}
              onSaved={() => {
                setPopoverDate(null);
                onOverrideChange();
              }}
            />
          </div>
        </div>
      )}
    </div>
  );
}

function OverridePopoverForm({
  scheduleId,
  date,
  existing,
  directory,
  token,
  onClose,
  onSaved,
}: {
  scheduleId: string;
  date: string;
  existing: { id: string; userId: string } | null;
  directory: UserSummary[];
  token: string | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [userId, setUserId] = useState(existing?.userId ?? directory[0]?.id ?? "");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    if (!userId) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.post(`/api/v1/settings/on-call-schedules/${scheduleId}/overrides`, { userId, date }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function remove() {
    if (!existing) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.del(`/api/v1/settings/on-call-schedules/${scheduleId}/overrides/${existing.id}`, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}
      <div className="field">
        <label htmlFor="override-user">{t("settings.onCallSchedule.override.analyst")}</label>
        <select id="override-user" className="select" value={userId} onChange={(e) => setUserId(e.target.value)}>
          {directory.map((u) => (
            <option key={u.id} value={u.id}>
              {u.name}
            </option>
          ))}
        </select>
      </div>
      <div className="row-actions" style={{ marginTop: 14 }}>
        <button type="button" className="btn btn-primary btn-sm" disabled={submitting || !userId} onClick={save}>
          {submitting ? t("common.saving") : t("settings.onCallSchedule.override.create")}
        </button>
        {existing && (
          <button type="button" className="btn btn-danger btn-sm" disabled={submitting} onClick={remove}>
            {t("settings.onCallSchedule.override.remove")}
          </button>
        )}
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose}>
          {t("common.cancel")}
        </button>
      </div>
    </div>
  );
}
