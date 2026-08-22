import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { mutationErrorMessage, useObject } from "../../api/hooks";
import type { RetentionConfig } from "../../types/api";

// Settings -> Data & Audit -> Retention: how long a CLOSED alert/incident
// stays in the tool before cmd/worker's sweepDataRetention permanently
// deletes it. Unlike every other config panel in this app (Storage/SMTP/
// Slack -- no saved row means the feature is off), GET here never returns
// null: retention is on by default (18 months each), so this panel always
// has real numbers to show, pre-filled even before an admin ever saves
// anything -- `existing.configured` just distinguishes that default from a
// value someone actually chose.
export function RetentionConfigPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: existing, loading, error, reload } = useObject<RetentionConfig>(["retention-config"], (tok) =>
    api.get<RetentionConfig>("/api/v1/settings/retention", tok),
  );

  const [alertMonths, setAlertMonths] = useState("18");
  const [incidentMonths, setIncidentMonths] = useState("18");
  const [submitting, setSubmitting] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (existing) {
      setAlertMonths(String(existing.alertRetentionMonths));
      setIncidentMonths(String(existing.incidentRetentionMonths));
    }
  }, [existing]);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setSaveError(null);
    setSaved(false);
    try {
      await api.put(
        "/api/v1/settings/retention",
        { alertRetentionMonths: Number(alertMonths), incidentRetentionMonths: Number(incidentMonths) },
        token,
      );
      setSaved(true);
      reload();
    } catch (err) {
      setSaveError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  if (loading) return <div className="panel"><div className="empty-state">{t("common.loading")}</div></div>;

  return (
    <form onSubmit={handleSubmit} className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.retention.title")}</h2>
        {existing && !existing.configured && (
          <span className="badge">{t("settings.retention.usingDefault")}</span>
        )}
      </div>
      <p className="helper-text" style={{ marginBottom: 8 }}>
        {t("settings.retention.helper")}
      </p>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.retention.warning")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {saveError && <div className="error-banner">{saveError}</div>}
      {saved && <div className="helper-text" style={{ color: "var(--success)", marginBottom: 12 }}>{t("settings.retention.saved")}</div>}

      <div className="form-grid">
        <div className="field">
          <label htmlFor="retention-alert-months">{t("settings.retention.alertRetentionMonths")}</label>
          <input
            id="retention-alert-months"
            className="input"
            type="number"
            min={0}
            step={1}
            value={alertMonths}
            onChange={(e) => setAlertMonths(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="retention-incident-months">{t("settings.retention.incidentRetentionMonths")}</label>
          <input
            id="retention-incident-months"
            className="input"
            type="number"
            min={0}
            step={1}
            value={incidentMonths}
            onChange={(e) => setIncidentMonths(e.target.value)}
            required
          />
        </div>
      </div>

      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
      </div>
    </form>
  );
}
