import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { OnCallSchedule, OnCallShift } from "../../types/onCallShifts";
import type { UserSummary } from "../../types/users";

const WEEKDAY_KEYS = ["sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"] as const;

function minutesToClock(minutes: number): string {
  const h = Math.floor(minutes / 60)
    .toString()
    .padStart(2, "0");
  const m = (minutes % 60).toString().padStart(2, "0");
  return `${h}:${m}`;
}

function clockToMinutes(clock: string): number {
  const [h, m] = clock.split(":").map(Number);
  return h * 60 + m;
}

// Settings -> On-Call Schedule: a weekly recurring roster (weekday + local
// time-of-day range) that cmd/ingest consults to auto-assign a newly
// received alert to whoever's on shift (see AlertService.EnableOnCallAutoAssign
// / backend OnCallShiftService.ResolveCurrentAnalyst). Overlapping shifts are
// allowed -- the backend picks a single deterministic winner -- so there's no
// client-side overlap validation here either.
export function OnCallShiftsPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [schedule, setSchedule] = useState<OnCallSchedule | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  // Tracks which shift's delete is pending a confirm click (id, or null for
  // none) -- an inline confirm/cancel pair instead of window.confirm(), which
  // some embedded browser contexts silently auto-dismiss with no visible
  // dialog at all, making delete look like it does nothing.
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const { data: directory } = useList<UserSummary>((tk) => api.get<UserSummary[]>("/api/v1/users/directory", tk));

  function load() {
    setLoading(true);
    api
      .get<OnCallSchedule>("/api/v1/settings/on-call-shifts", token)
      .then(setSchedule)
      .catch((err: unknown) => setError(mutationErrorMessage(err)))
      .finally(() => setLoading(false));
  }

  useEffect(load, [token]);

  async function remove(id: string) {
    setDeletingId(id);
    setDeleteError(null);
    try {
      await api.del(`/api/v1/settings/on-call-shifts/${id}`, token);
      setConfirmingId(null);
      load();
    } catch (err) {
      setDeleteError(mutationErrorMessage(err));
    } finally {
      setDeletingId(null);
    }
  }

  if (loading) {
    return (
      <div className="panel">
        <div className="empty-state">{t("common.loading")}</div>
      </div>
    );
  }

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.onCallShifts.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("settings.onCallShifts.newShift")}
        </button>
      </div>
      <p className="helper-text" style={{ marginTop: -8, marginBottom: 14 }}>
        {t("settings.onCallShifts.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {deleteError && <div className="error-banner">{deleteError}</div>}

      <TimezoneField timezone={schedule?.timezone ?? "UTC"} onSaved={load} />

      {showCreate && (
        <CreateShiftForm
          directory={directory ?? []}
          onCancel={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false);
            load();
          }}
        />
      )}

      {schedule && schedule.shifts.length === 0 && <div className="empty-state">{t("settings.onCallShifts.noShifts")}</div>}
      {schedule &&
        schedule.shifts.map((shift: OnCallShift) => (
          <div className="row" key={shift.id}>
            <div className="row-main">
              <p className="row-title">{shift.userName}</p>
              <p className="row-sub">
                {t(`settings.onCallShifts.weekday.${WEEKDAY_KEYS[shift.weekday]}`)} · {minutesToClock(shift.startMinute)}–
                {minutesToClock(shift.endMinute)}
              </p>
            </div>
            <div className="row-actions">
              {confirmingId === shift.id ? (
                <>
                  <span className="helper-text">{t("settings.onCallShifts.deleteConfirm")}</span>
                  <button
                    className="btn btn-danger btn-sm"
                    disabled={deletingId === shift.id}
                    onClick={() => remove(shift.id)}
                  >
                    {deletingId === shift.id ? t("common.saving") : t("common.confirmDelete")}
                  </button>
                  <button className="btn btn-ghost btn-sm" onClick={() => setConfirmingId(null)}>
                    {t("common.cancel")}
                  </button>
                </>
              ) : (
                <button className="btn btn-danger btn-sm" onClick={() => setConfirmingId(shift.id)}>
                  {t("settings.onCallShifts.delete")}
                </button>
              )}
            </div>
          </div>
        ))}
    </div>
  );
}

function TimezoneField({ timezone, onSaved }: { timezone: string; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [value, setValue] = useState(timezone);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => setValue(timezone), [timezone]);

  async function save() {
    setSubmitting(true);
    setError(null);
    try {
      await api.put("/api/v1/settings/on-call-shifts/timezone", { timezone: value }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  const dirty = value !== timezone;

  return (
    <div className="field" style={{ marginBottom: 16, maxWidth: 320 }}>
      <label htmlFor="oncall-timezone">{t("settings.onCallShifts.timezone")}</label>
      {error && <div className="error-banner">{error}</div>}
      <div style={{ display: "flex", gap: 8 }}>
        <input
          id="oncall-timezone"
          className="input"
          style={{ flex: 1 }}
          placeholder="America/Sao_Paulo"
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
        {dirty && (
          <button type="button" className="btn btn-sm" disabled={submitting} onClick={save}>
            {submitting ? t("common.saving") : t("common.save")}
          </button>
        )}
      </div>
    </div>
  );
}

function CreateShiftForm({
  directory,
  onCancel,
  onCreated,
}: {
  directory: UserSummary[];
  onCancel: () => void;
  onCreated: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [userId, setUserId] = useState(directory[0]?.id ?? "");
  const [weekday, setWeekday] = useState(1);
  const [startClock, setStartClock] = useState("09:00");
  const [endClock, setEndClock] = useState("17:00");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!userId) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.post(
        "/api/v1/settings/on-call-shifts",
        { userId, weekday, startMinute: clockToMinutes(startClock), endMinute: clockToMinutes(endClock) },
        token,
      );
      onCreated();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 14 }}>
      {error && <div className="error-banner">{error}</div>}
      <div className="form-grid">
        <div className="field">
          <label htmlFor="shift-analyst">{t("settings.onCallShifts.form.analyst")}</label>
          <select id="shift-analyst" className="select" value={userId} onChange={(e) => setUserId(e.target.value)} required>
            {directory.map((u) => (
              <option key={u.id} value={u.id}>
                {u.name}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label htmlFor="shift-weekday">{t("settings.onCallShifts.form.weekday")}</label>
          <select id="shift-weekday" className="select" value={weekday} onChange={(e) => setWeekday(Number(e.target.value))}>
            {WEEKDAY_KEYS.map((key, idx) => (
              <option key={key} value={idx}>
                {t(`settings.onCallShifts.weekday.${key}`)}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label htmlFor="shift-start">{t("settings.onCallShifts.form.start")}</label>
          <input id="shift-start" type="time" className="input" value={startClock} onChange={(e) => setStartClock(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="shift-end">{t("settings.onCallShifts.form.end")}</label>
          <input id="shift-end" type="time" className="input" value={endClock} onChange={(e) => setEndClock(e.target.value)} required />
        </div>
      </div>
      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting || !userId}>
          {submitting ? t("common.creating") : t("common.create")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}
