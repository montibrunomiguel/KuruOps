import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { Severity } from "../../../types/alerts";
import type { Incident, IncidentPriority } from "../../../types/incidents";
import { PRIORITY_ORDER } from "../../../lib/chartColors";

const MATRIX_SEVERITIES: Severity[] = ["critical", "high", "medium", "low", "informational"];

export function NistMatrixPanel({ incident, onSaved }: { incident: Incident; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [submitting, setSubmitting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function apply(severity: Severity, priority: IncidentPriority) {
    if (incident.severity === severity && incident.priority === priority) return;
    const key = `${severity}-${priority}`;
    setSubmitting(key);
    setError(null);
    try {
      await api.post(`/api/v1/incidents/${incident.id}/severity-priority`, { severity, priority }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(null);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 4 }}>
        {t("incidents.detail.matrixTitle")}
      </h2>
      <p className="helper-text" style={{ marginBottom: 12 }}>
        {t("incidents.detail.matrixHint")}
      </p>
      {error && <div className="error-banner">{error}</div>}
      <div className="nist-matrix">
        <span />
        {PRIORITY_ORDER.map((p) => (
          <span className="nist-matrix-header-cell" key={p}>
            {p.toUpperCase()}
          </span>
        ))}
        {MATRIX_SEVERITIES.map((sev) => (
          <>
            <span className="nist-matrix-row-label" key={`label-${sev}`}>
              {t(`common.severity.${sev}`)}
            </span>
            {PRIORITY_ORDER.map((p) => {
              const active = incident.severity === sev && incident.priority === p;
              const key = `${sev}-${p}`;
              return (
                <button
                  type="button"
                  className="nist-matrix-cell"
                  key={key}
                  data-active={active}
                  disabled={submitting === key}
                  onClick={() => apply(sev, p)}
                  aria-label={`${t(`common.severity.${sev}`)} / ${p.toUpperCase()}`}
                >
                  {active && <span className="nist-matrix-cell-dot" />}
                </button>
              );
            })}
          </>
        ))}
      </div>
    </div>
  );
}
