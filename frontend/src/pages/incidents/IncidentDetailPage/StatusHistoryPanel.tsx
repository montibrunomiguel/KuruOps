import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { IncidentPhase, IncidentStatusHistoryEntry } from "../../../types/incidents";
import { NIST_PHASE_ORDER } from "../../../types/incidents";
import { formatDateTime, shortId } from "../../../lib/format";

// StatusHistoryPanel surfaces incident_status_history -- one row per NIST
// phase the incident has entered, per db/migrations/0005_incidents.up.sql.
// entered_at is never edited directly (audit/chain-of-custody requirement):
// a correction writes corrected_entered_at + who/why into the same row,
// alongside a status_timestamp_corrected event in the append-only
// incident_events log (see CorrectPhaseTimestamp in incident_service.go).
// The datetime-local input is always visible per row (no toggle button) --
// the reason field only appears once its value actually diverges from the
// entry's current effective time, same dirty-tracking principle as
// TagsEditPanel/SeverityPriorityPanel below.
export function StatusHistoryPanel({
  incidentId,
  currentPhase,
  entries,
  onCorrected,
}: {
  incidentId: string;
  currentPhase: IncidentPhase;
  entries: IncidentStatusHistoryEntry[];
  onCorrected: () => void;
}) {
  const { t } = useTranslation();
  const byPhase = new Map(entries.map((e) => [e.phase, e]));

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 12 }}>
        {t("incidents.detail.statusHistoryTitle")}
      </h2>
      {NIST_PHASE_ORDER.filter((p) => byPhase.has(p)).map((phase) => (
        <StatusHistoryRow
          key={phase}
          incidentId={incidentId}
          phase={phase}
          entry={byPhase.get(phase)!}
          isCurrent={phase === currentPhase}
          onCorrected={onCorrected}
        />
      ))}
    </div>
  );
}

function toDatetimeLocal(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function StatusHistoryRow({
  incidentId,
  phase,
  entry,
  isCurrent,
  onCorrected,
}: {
  incidentId: string;
  phase: IncidentPhase;
  entry: IncidentStatusHistoryEntry;
  isCurrent: boolean;
  onCorrected: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const effective = entry.correctedEnteredAt ?? entry.enteredAt;
  const [value, setValue] = useState(() => toDatetimeLocal(effective));
  const [reason, setReason] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const dirty = value !== toDatetimeLocal(effective);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!reason.trim()) {
      setError(t("incidents.detail.reasonRequired"));
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await api.post(
        `/api/v1/incidents/${incidentId}/status-history/${phase}/correct`,
        { enteredAt: new Date(value).toISOString(), reason },
        token,
      );
      setReason("");
      onCorrected();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form className="row" style={{ display: "block" }} onSubmit={save}>
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 10, marginBottom: dirty ? 8 : 0 }}>
        <span style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12.5, fontWeight: 600 }}>
          <span className="legend-dot" style={{ background: isCurrent ? "var(--accent)" : "var(--success)" }} />
          {t(`common.phase.${phase}`)}
        </span>
        <input
          type="datetime-local"
          className="input"
          style={{ fontSize: 11.5, padding: "4px 8px", minWidth: 0 }}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          aria-label={`${t("incidents.detail.correctTime")} — ${t(`common.phase.${phase}`)}`}
        />
      </div>
      {entry.correctedEnteredAt && (
        <p className="row-sub" style={{ marginTop: 4 }}>
          {t("incidents.detail.originalWas", {
            date: formatDateTime(entry.enteredAt),
            by: entry.correctedBy ? shortId(entry.correctedBy) : "—",
          })}
        </p>
      )}
      {dirty && (
        <div style={{ marginTop: 8 }}>
          {error && <div className="error-banner">{error}</div>}
          <textarea
            className="textarea"
            style={{ width: "100%", minHeight: 50 }}
            placeholder={t("incidents.detail.correctionReasonPlaceholder")}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
          <div className="row-actions" style={{ marginTop: 8 }}>
            <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
              {submitting ? t("common.saving") : t("incidents.detail.saveCorrection")}
            </button>
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => {
                setValue(toDatetimeLocal(effective));
                setReason("");
                setError(null);
              }}
            >
              {t("common.cancel")}
            </button>
          </div>
        </div>
      )}
    </form>
  );
}
