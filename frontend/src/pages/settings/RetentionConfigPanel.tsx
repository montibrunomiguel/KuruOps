import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useAdminSingletonConfig } from "../../hooks/useAdminSingletonConfig";
import { useConfirm } from "../../hooks/useConfirm";
import type { RetentionConfig } from "../../types/api";

// Settings -> Data & Audit -> Retention: how long a CLOSED alert/incident
// stays in the tool before cmd/worker's sweepDataRetention permanently
// deletes it. Unlike every other config panel in this app (Storage/SMTP/
// Slack -- no saved row means the feature is off), GET here never returns
// null: retention is on by default (18 months each), so this panel always
// has real numbers to show, pre-filled even before an admin ever saves
// anything -- `existing.configured` just distinguishes that default from a
// value someone actually chose (a different thing from useAdminSingletonConfig's
// own `configured`, which just means "loaded without error").
export function RetentionConfigPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: existing, loading, error, saveError, submitting, saved, save } = useAdminSingletonConfig<RetentionConfig>(
    ["retention-config"],
    (tok) => api.get<RetentionConfig>("/api/v1/settings/retention", tok),
  );

  const [alertMonths, setAlertMonths] = useState("18");
  const [incidentMonths, setIncidentMonths] = useState("18");
  // Lowering either value below what's currently in effect queues an
  // existing closed alert/incident for permanent deletion on the next
  // sweep -- gated behind the same inline-confirm pattern every other
  // irreversible action in this app uses (see useConfirm's doc comment),
  // unlike raising a value or saving for the first time, which is always
  // harmless and needs no confirmation.
  const { confirming: confirmingLower, confirm: confirmLower, cancel: cancelLower } = useConfirm();

  useEffect(() => {
    if (existing) {
      setAlertMonths(String(existing.alertRetentionMonths));
      setIncidentMonths(String(existing.incidentRetentionMonths));
    }
  }, [existing]);

  function isLoweringRetention(): boolean {
    if (!existing) return false;
    return Number(alertMonths) < existing.alertRetentionMonths || Number(incidentMonths) < existing.incidentRetentionMonths;
  }

  // Editing either field after a "lower value" confirmation is already
  // showing invalidates it -- otherwise clicking "Save anyway" after a
  // last-second edit could save a since-changed value under a warning that
  // was computed for the old one.
  function handleMonthsChange(setter: (v: string) => void, value: string) {
    setter(value);
    if (confirmingLower) cancelLower();
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (isLoweringRetention() && !confirmingLower) {
      confirmLower();
      return;
    }
    void doSave();
  }

  async function doSave() {
    cancelLower();
    await save(async () => {
      await api.put(
        "/api/v1/settings/retention",
        { alertRetentionMonths: Number(alertMonths), incidentRetentionMonths: Number(incidentMonths) },
        token,
      );
    });
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
      {confirmingLower && <div className="error-banner">{t("settings.retention.lowerConfirmBanner")}</div>}

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
            onChange={(e) => handleMonthsChange(setAlertMonths, e.target.value)}
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
            onChange={(e) => handleMonthsChange(setIncidentMonths, e.target.value)}
            required
          />
        </div>
      </div>

      <div className="row-actions">
        {!confirmingLower ? (
          <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
            {submitting ? t("common.saving") : t("common.save")}
          </button>
        ) : (
          <>
            <button type="submit" className="btn btn-danger btn-sm" disabled={submitting}>
              {submitting ? t("common.saving") : t("settings.retention.confirmLower")}
            </button>
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => cancelLower()} disabled={submitting}>
              {t("common.cancel")}
            </button>
          </>
        )}
      </div>
    </form>
  );
}
