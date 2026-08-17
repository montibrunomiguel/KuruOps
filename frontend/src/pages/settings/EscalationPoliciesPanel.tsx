import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../api/client";
import { useList } from "../../api/hooks";
import type { EscalationPolicy } from "../../types/api";
import type { Severity } from "../../types/alerts";
import type { OnCallSchedule } from "../../types/onCallSchedule";
import { EscalationRow } from "./EscalationPoliciesPanel/EscalationRow";

const SEVERITIES: Severity[] = ["critical", "high", "medium", "low", "informational"];

// Settings -> Escala de Acionamento: an overview table (one row per
// severity) instead of five permanently-expanded forms -- Configured/Not
// configured at a glance, the chain's steps summarized without opening
// anything. Editing a row expands it in place into an ordered step editor
// (add/remove/reorder, same up/down-arrow pattern Escala de Atendimento's
// responder list uses); each saved step also gets a "Send test" action
// (mirrors SMTPConfigPanel's "Send test email") so an admin can confirm
// delivery -- and, for webhook, a custom payload template with the resolved
// on-call analyst's name/email/phone -- works before waiting on a real
// alert to escalate.
export function EscalationPoliciesPanel() {
  const { t } = useTranslation();
  const { data: policies, loading, error, reload } = useList<EscalationPolicy>((tk) =>
    api.get<EscalationPolicy[]>("/api/v1/settings/escalation-policies", tk),
  );
  const { data: schedules } = useList<OnCallSchedule>((tk) => api.get<OnCallSchedule[]>("/api/v1/settings/on-call-schedules", tk));
  const [editingSeverity, setEditingSeverity] = useState<Severity | null>(null);

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 4 }}>
        {t("settings.escalation.title")}
      </h2>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.escalation.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {loading && <div className="empty-state">{t("common.loading")}</div>}

      {!loading && schedules && schedules.length === 0 && (
        <div className="empty-state">{t("settings.escalation.noSchedules")}</div>
      )}

      {!loading && schedules && schedules.length > 0 && (
        <table className="table">
          <thead>
            <tr>
              <th>{t("settings.escalation.columns.severity")}</th>
              <th>{t("settings.escalation.columns.status")}</th>
              <th>{t("settings.escalation.columns.chain")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {SEVERITIES.map((severity) => (
              <EscalationRow
                key={severity}
                severity={severity}
                policy={policies?.find((p) => p.severity === severity) ?? null}
                schedules={schedules}
                editing={editingSeverity === severity}
                onEdit={() => setEditingSeverity(severity)}
                onCancelEdit={() => setEditingSeverity(null)}
                onChanged={() => {
                  setEditingSeverity(null);
                  reload();
                }}
              />
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
