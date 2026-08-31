import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import { useConfirm } from "../../../hooks/useConfirm";
import type { OnCallSchedule, OnCallWorkingHoursInterval, OnCallWorkingHoursMode, SaveOnCallScheduleRequest } from "../../../types/onCallSchedule";
import type { UserSummary } from "../../../types/users";
import { personColor } from "../../../lib/personColor";

const WEEKDAY_KEYS = ["sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"] as const;

// A plain fixed-offset picker (GMT-12 .. GMT+14) instead of a free-text IANA
// name -- simpler for an admin to pick correctly, at the cost of not
// tracking DST transitions. Etc/GMT zone names use POSIX's inverted sign
// convention (Etc/GMT-5 is UTC+5), so the mapping from the human-facing
// "GMT+N" label to the IANA value flips the sign; time.LoadLocation on the
// backend accepts these the same as any other IANA name, no backend change
// needed.
function buildTimezoneOptions(): { value: string; label: string }[] {
  const opts: { value: string; label: string }[] = [];
  for (let offset = -12; offset <= 14; offset++) {
    if (offset === 0) {
      opts.push({ value: "UTC", label: "GMT+0" });
      continue;
    }
    const value = offset > 0 ? `Etc/GMT-${offset}` : `Etc/GMT+${Math.abs(offset)}`;
    opts.push({ value, label: `GMT${offset > 0 ? "+" : ""}${offset}` });
  }
  return opts;
}
const TIMEZONE_OPTIONS = buildTimezoneOptions();

function minutesToClock(minutes: number): string {
  const h = Math.floor(minutes / 60).toString().padStart(2, "0");
  const m = (minutes % 60).toString().padStart(2, "0");
  return `${h}:${m}`;
}

function clockToMinutes(clock: string): number {
  const [h, m] = clock.split(":").map(Number);
  return h * 60 + m;
}

function toDatetimeLocal(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => n.toString().padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// See workingHours' useState call below for why key exists -- stripped
// again before this ever reaches SaveOnCallScheduleRequest.
interface EditableWorkingHours extends OnCallWorkingHoursInterval {
  key: string;
}

type Cadence = "daily" | "weekly" | "custom";

function cadenceFor(periodDays: number): Cadence {
  if (periodDays === 1) return "daily";
  if (periodDays === 7) return "weekly";
  return "custom";
}

export function ScheduleForm({
  schedule,
  isNew,
  directory,
  onSaved,
  token,
  navigate,
}: {
  schedule: OnCallSchedule | null;
  isNew: boolean;
  directory: UserSummary[];
  onSaved: () => void;
  token: string | null;
  navigate: ReturnType<typeof useNavigate>;
}) {
  const { t } = useTranslation();

  const [name, setName] = useState(schedule?.name ?? "");
  const [participantIds, setParticipantIds] = useState<string[]>(schedule?.participants.map((p) => p.userId) ?? []);
  const [addUserId, setAddUserId] = useState("");
  const [handoverAt, setHandoverAt] = useState(schedule ? toDatetimeLocal(schedule.handoverAt) : toDatetimeLocal(new Date().toISOString()));
  const [timezone, setTimezone] = useState(
    () => TIMEZONE_OPTIONS.find((o) => o.value === schedule?.timezone)?.value ?? "UTC",
  );
  const [cadence, setCadence] = useState<Cadence>(cadenceFor(schedule?.periodDays ?? 7));
  const [customPeriodDays, setCustomPeriodDays] = useState(schedule?.periodDays ?? 7);
  const [concurrentShifts, setConcurrentShifts] = useState(schedule?.concurrentShifts ?? 1);
  const [workingHoursMode, setWorkingHoursMode] = useState<OnCallWorkingHoursMode>(schedule?.workingHoursMode ?? "all_day");
  // EditableWorkingHours adds a client-only React list key (crypto.randomUUID(),
  // stripped again in save() below) -- OnCallWorkingHoursInterval itself has
  // no id (it's a plain interval, not an entity with backend identity), and
  // this list is reorderable/removable via addWorkingHours/removeWorkingHours,
  // so a positional key would misattribute a row's DOM/focus state after a
  // removal shifts every later row's index.
  const [workingHours, setWorkingHours] = useState<EditableWorkingHours[]>(
    (schedule?.workingHours ?? []).map((iv) => ({ ...iv, key: crypto.randomUUID() })),
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { confirming: confirmingDelete, confirm: confirmDelete, cancel: cancelDelete } = useConfirm();

  const participantsById = new Map(directory.map((u) => [u.id, u]));
  const availableToAdd = directory.filter((u) => !participantIds.includes(u.id));
  // addUserId only tracks an explicit selection -- once it stops being a
  // valid choice (added, or the very first render), fall back to the first
  // still-available directory entry instead of hanging onto a stale id that
  // no option in the <select> can represent anymore.
  const selectedAddUserId = availableToAdd.some((u) => u.id === addUserId) ? addUserId : availableToAdd[0]?.id ?? "";

  function addParticipant() {
    if (!selectedAddUserId) return;
    setParticipantIds((ids) => [...ids, selectedAddUserId]);
    setAddUserId("");
  }

  function removeParticipant(userId: string) {
    setParticipantIds((ids) => ids.filter((id) => id !== userId));
  }

  function reorder(from: number, to: number) {
    setParticipantIds((ids) => {
      const next = [...ids];
      const [moved] = next.splice(from, 1);
      next.splice(to, 0, moved);
      return next;
    });
  }

  function moveParticipant(idx: number, direction: -1 | 1) {
    const target = idx + direction;
    if (target < 0 || target >= participantIds.length) return;
    reorder(idx, target);
  }

  function addWorkingHours() {
    setWorkingHours((rows) => [...rows, { key: crypto.randomUUID(), weekdays: [1, 2, 3, 4, 5], startMinute: 9 * 60, endMinute: 17 * 60 }]);
  }

  function updateWorkingHours(idx: number, patch: Partial<OnCallWorkingHoursInterval>) {
    setWorkingHours((rows) => rows.map((r, i) => (i === idx ? { ...r, ...patch } : r)));
  }

  function toggleWeekday(idx: number, weekday: number) {
    setWorkingHours((rows) =>
      rows.map((r, i) => {
        if (i !== idx) return r;
        const has = r.weekdays.includes(weekday);
        return { ...r, weekdays: has ? r.weekdays.filter((w) => w !== weekday) : [...r.weekdays, weekday].sort() };
      }),
    );
  }

  function removeWorkingHours(idx: number) {
    setWorkingHours((rows) => rows.filter((_, i) => i !== idx));
  }

  async function save() {
    if (!name.trim()) {
      setError(t("settings.onCallSchedule.form.nameRequired"));
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const periodDays = cadence === "daily" ? 1 : cadence === "weekly" ? 7 : customPeriodDays;
      const body: SaveOnCallScheduleRequest = {
        name: name.trim(),
        participantIds,
        handoverAt: new Date(handoverAt).toISOString(),
        periodDays,
        concurrentShifts,
        workingHoursMode,
        workingHours:
          workingHoursMode === "specific_times"
            ? workingHours.map((iv) => ({ id: iv.id, weekdays: iv.weekdays, startMinute: iv.startMinute, endMinute: iv.endMinute }))
            : [],
      };
      if (isNew) {
        const created = await api.post<OnCallSchedule>("/api/v1/settings/on-call-schedules", body, token);
        await api.put("/api/v1/settings/on-call-schedules/timezone", { timezone }, token);
        navigate(`/settings/on-call-schedules/${created.id}`, { replace: true });
      } else if (schedule) {
        await api.put(`/api/v1/settings/on-call-schedules/${schedule.id}`, body, token);
        await api.put("/api/v1/settings/on-call-schedules/timezone", { timezone }, token);
        onSaved();
      }
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function remove() {
    if (!schedule) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.del(`/api/v1/settings/on-call-schedules/${schedule.id}`, token);
      navigate("/settings/on-call-schedules", { replace: true });
    } catch (err) {
      setError(mutationErrorMessage(err));
      cancelDelete();
      setSubmitting(false);
    }
  }

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}

      <div className="form-grid">
        <div className="field">
          <label htmlFor="oncall-name">{t("settings.onCallSchedule.form.name")}</label>
          <input id="oncall-name" className="input" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="oncall-handover">{t("settings.onCallSchedule.form.handoverAt")}</label>
          <input
            id="oncall-handover"
            type="datetime-local"
            className="input"
            value={handoverAt}
            onChange={(e) => setHandoverAt(e.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor="oncall-timezone">{t("settings.onCallSchedule.form.timezone")}</label>
          <select
            id="oncall-timezone"
            className="select"
            value={timezone}
            onChange={(e) => setTimezone(e.target.value)}
          >
            {TIMEZONE_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label htmlFor="oncall-concurrent">{t("settings.onCallSchedule.form.concurrentShifts")}</label>
          <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => setConcurrentShifts((n) => Math.max(1, n - 1))}
              aria-label={t("settings.onCallSchedule.form.decreaseConcurrent")}
            >
              −
            </button>
            <input
              id="oncall-concurrent"
              className="input"
              style={{ width: 56, textAlign: "center" }}
              type="number"
              min={1}
              value={concurrentShifts}
              onChange={(e) => setConcurrentShifts(Number(e.target.value))}
            />
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => setConcurrentShifts((n) => n + 1)}
              aria-label={t("settings.onCallSchedule.form.increaseConcurrent")}
            >
              +
            </button>
          </div>
        </div>
      </div>

      <div className="field" style={{ marginTop: 10 }}>
        <label>{t("settings.onCallSchedule.form.cadence")}</label>
        <div style={{ display: "flex", gap: 14, alignItems: "center", flexWrap: "wrap" }}>
          {(["daily", "weekly", "custom"] as Cadence[]).map((c) => (
            <label key={c} style={{ display: "flex", gap: 6, alignItems: "center", fontWeight: 400 }}>
              <input type="radio" name="cadence" checked={cadence === c} onChange={() => setCadence(c)} />
              {t(`settings.onCallSchedule.form.cadenceOption.${c}`)}
            </label>
          ))}
          {cadence === "custom" && (
            <input
              type="number"
              min={1}
              className="input"
              style={{ width: 90 }}
              value={customPeriodDays}
              onChange={(e) => setCustomPeriodDays(Number(e.target.value))}
              aria-label={t("settings.onCallSchedule.form.customPeriodDays")}
            />
          )}
        </div>
      </div>

      <div className="field" style={{ marginTop: 14 }}>
        <label>{t("settings.onCallSchedule.form.responders")}</label>
        <p className="helper-text" style={{ marginTop: -2, marginBottom: 6 }}>
          {t("settings.onCallSchedule.form.respondersHelp")}
        </p>
        {participantIds.map((userId, idx) => (
          <div key={userId} className="row" style={{ padding: "6px 10px", marginBottom: 4 }}>
            <div className="row-main" style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span
                aria-hidden
                style={{
                  display: "inline-block",
                  width: 10,
                  height: 10,
                  borderRadius: "50%",
                  background: personColor(userId),
                }}
              />
              <span>{participantsById.get(userId)?.name ?? userId}</span>
            </div>
            <div className="row-actions">
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => moveParticipant(idx, -1)}
                disabled={idx === 0}
                aria-label={t("settings.onCallSchedule.form.moveUp")}
              >
                ↑
              </button>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => moveParticipant(idx, 1)}
                disabled={idx === participantIds.length - 1}
                aria-label={t("settings.onCallSchedule.form.moveDown")}
              >
                ↓
              </button>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => removeParticipant(userId)}
                aria-label={t("settings.onCallSchedule.form.removeResponder")}
              >
                ×
              </button>
            </div>
          </div>
        ))}
        {availableToAdd.length > 0 && (
          <div style={{ display: "flex", gap: 8, marginTop: 6 }}>
            <select
              className="select"
              aria-label={t("settings.onCallSchedule.form.addResponderAria")}
              value={selectedAddUserId}
              onChange={(e) => setAddUserId(e.target.value)}
            >
              {availableToAdd.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name}
                </option>
              ))}
            </select>
            <button type="button" className="btn btn-sm" onClick={addParticipant}>
              {t("settings.onCallSchedule.form.addResponder")}
            </button>
          </div>
        )}
      </div>

      <div className="field" style={{ marginTop: 14 }}>
        <label>{t("settings.onCallSchedule.form.workingHours")}</label>
        <div style={{ display: "flex", gap: 14, marginBottom: 8 }}>
          <label style={{ display: "flex", gap: 6, alignItems: "center", fontWeight: 400 }}>
            <input
              type="radio"
              name="workingHoursMode"
              checked={workingHoursMode === "all_day"}
              onChange={() => setWorkingHoursMode("all_day")}
            />
            {t("settings.onCallSchedule.form.allDay")}
          </label>
          <label style={{ display: "flex", gap: 6, alignItems: "center", fontWeight: 400 }}>
            <input
              type="radio"
              name="workingHoursMode"
              checked={workingHoursMode === "specific_times"}
              onChange={() => setWorkingHoursMode("specific_times")}
            />
            {t("settings.onCallSchedule.form.specificTimes")}
          </label>
        </div>

        {workingHoursMode === "specific_times" && (
          <div>
            {workingHours.map((iv, idx) => (
              <div key={iv.key} style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 6, flexWrap: "wrap" }}>
                <div style={{ display: "flex", gap: 2 }}>
                  {WEEKDAY_KEYS.map((key, weekday) => (
                    <button
                      key={key}
                      type="button"
                      className={iv.weekdays.includes(weekday) ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"}
                      style={{ padding: "2px 6px", fontSize: 11 }}
                      onClick={() => toggleWeekday(idx, weekday)}
                      title={t(`settings.onCallSchedule.weekday.${key}`)}
                    >
                      {t(`settings.onCallSchedule.weekday.${key}`).slice(0, 1)}
                    </button>
                  ))}
                </div>
                <input
                  type="time"
                  className="input"
                  style={{ width: 110 }}
                  aria-label={t("settings.onCallSchedule.form.startTimeAria", { num: idx + 1 })}
                  value={minutesToClock(iv.startMinute)}
                  onChange={(e) => updateWorkingHours(idx, { startMinute: clockToMinutes(e.target.value) })}
                />
                <span>–</span>
                <input
                  type="time"
                  className="input"
                  style={{ width: 110 }}
                  aria-label={t("settings.onCallSchedule.form.endTimeAria", { num: idx + 1 })}
                  value={minutesToClock(iv.endMinute)}
                  onChange={(e) => updateWorkingHours(idx, { endMinute: clockToMinutes(e.target.value) })}
                />
                <button
                  type="button"
                  className="btn btn-ghost btn-sm"
                  onClick={() => removeWorkingHours(idx)}
                  aria-label={t("settings.onCallSchedule.form.removeInterval")}
                >
                  ×
                </button>
              </div>
            ))}
            <button type="button" className="btn btn-sm" onClick={addWorkingHours}>
              {t("settings.onCallSchedule.form.addInterval")}
            </button>
          </div>
        )}
      </div>

      <div className="row-actions" style={{ marginTop: 16 }}>
        <button type="button" className="btn btn-primary btn-sm" disabled={submitting} onClick={save}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
        {!isNew &&
          (confirmingDelete ? (
            <>
              <button type="button" className="btn btn-danger btn-sm" disabled={submitting} onClick={remove}>
                {submitting ? t("common.saving") : t("common.confirmDelete")}
              </button>
              <button type="button" className="btn btn-ghost btn-sm" onClick={cancelDelete}>
                {t("common.cancel")}
              </button>
            </>
          ) : (
            <button type="button" className="btn btn-danger btn-sm" disabled={submitting} onClick={() => confirmDelete()}>
              {t("settings.onCallSchedule.deleteSchedule")}
            </button>
          ))}
      </div>
    </div>
  );
}
