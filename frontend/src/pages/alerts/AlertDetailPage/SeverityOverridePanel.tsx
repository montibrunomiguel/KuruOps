import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { Alert, Severity } from "../../../types/alerts";

// Severity only -- status used to live here too (a second dropdown), but
// every status transition now has its own dedicated action: "Start
// Investigation" in the toolbar (open -> investigating), "Escalate to
// Incident" (-> escalated), "Close & Classify" (-> closed). A free-standing
// status dropdown let an analyst silently mark an alert "escalated" without
// actually creating/linking an incident, which was actively misleading.
export function SeverityOverridePanel({ alert, onSaved }: { alert: Alert; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [severity, setSeverity] = useState<Severity>(alert.severity);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const readOnly = alert.status === "closed";
  const dirty = !readOnly && severity !== alert.severity;

  async function save() {
    setSubmitting(true);
    setError(null);
    try {
      await api.put(`/api/v1/alerts/${alert.id}/severity`, { severity }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.severityOverrideTitle")}
      </h2>
      {error && <div className="error-banner">{error}</div>}
      <p className="helper-text" style={{ marginBottom: 10 }}>
        {t("alerts.detail.originalSeverity", { severity: t(`common.severity.${alert.originalSeverity}`) })}
      </p>
      <div className="field">
        <select
          className="select"
          style={{ width: "100%" }}
          aria-label={t("alerts.detail.severityOverrideTitle")}
          value={severity}
          disabled={readOnly}
          onChange={(e) => setSeverity(e.target.value as Severity)}
        >
          <option value="critical">{t("common.severity.critical")}</option>
          <option value="high">{t("common.severity.high")}</option>
          <option value="medium">{t("common.severity.medium")}</option>
          <option value="low">{t("common.severity.low")}</option>
          <option value="informational">{t("common.severity.informational")}</option>
        </select>
      </div>
      {dirty && (
        <button className="btn btn-primary btn-sm" style={{ width: "100%" }} disabled={submitting} onClick={save}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
      )}
    </div>
  );
}
