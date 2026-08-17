import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import { useConfirm } from "../../../hooks/useConfirm";
import type { EscalationPolicy } from "../../../types/api";
import type { Severity } from "../../../types/alerts";
import type { OnCallSchedule } from "../../../types/onCallSchedule";
import { EscalationEditForm } from "./EscalationEditForm";

export function EscalationRow({
  severity,
  policy,
  schedules,
  editing,
  onEdit,
  onCancelEdit,
  onChanged,
}: {
  severity: Severity;
  policy: EscalationPolicy | null;
  schedules: OnCallSchedule[];
  editing: boolean;
  onEdit: () => void;
  onCancelEdit: () => void;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { confirming: confirmingRemove, confirm, cancel } = useConfirm();
  const [removing, setRemoving] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);
  const [testingPosition, setTestingPosition] = useState<number | null>(null);
  const [testResult, setTestResult] = useState<{ position: number; ok: boolean; error?: string } | null>(null);

  async function remove() {
    if (!policy) return;
    setRemoving(true);
    setRemoveError(null);
    try {
      await api.del(`/api/v1/settings/escalation-policies/${policy.id}`, token);
      cancel();
      onChanged();
    } catch (err) {
      setRemoveError(mutationErrorMessage(err));
    } finally {
      setRemoving(false);
    }
  }

  async function sendTest(position: number) {
    setTestingPosition(position);
    setTestResult(null);
    try {
      await api.post("/api/v1/settings/escalation-policies/test", { severity, stepPosition: position }, token);
      setTestResult({ position, ok: true });
    } catch (err) {
      setTestResult({ position, ok: false, error: mutationErrorMessage(err) });
    } finally {
      setTestingPosition(null);
    }
  }

  if (editing) {
    return (
      <tr>
        <td colSpan={4}>
          <EscalationEditForm severity={severity} policy={policy} schedules={schedules} onCancel={onCancelEdit} onSaved={onChanged} />
        </td>
      </tr>
    );
  }

  return (
    <tr>
      <td>
        <strong>{t(`common.severity.${severity}`)}</strong>
      </td>
      <td>
        {policy ? (
          <span className="badge badge-success">
            <span className="badge-status-dot" />
            {t("settings.escalation.configured")}
          </span>
        ) : (
          <span className="field-hint">{t("settings.escalation.notConfigured")}</span>
        )}
      </td>
      <td>
        {policy ? (
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            {policy.steps.map((step, idx) => (
              <div key={step.id} style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12.5 }}>
                <span className="badge badge-muted">{idx + 1}</span>
                <span>{step.scheduleName}</span>
                <span className="field-hint">
                  {t("settings.escalation.stepSummary", { minutes: step.delayMinutes, channel: t(`settings.escalation.form.channelOption.${step.channelType}`) })}
                </span>
                <button
                  type="button"
                  className="btn btn-ghost btn-sm"
                  style={{ padding: "1px 8px" }}
                  onClick={() => sendTest(idx)}
                  disabled={testingPosition === idx}
                >
                  {testingPosition === idx ? t("settings.escalation.testing") : t("settings.escalation.test")}
                </button>
                {testResult?.position === idx && testResult.ok && (
                  <span className="helper-text" style={{ color: "var(--success)" }}>{t("settings.escalation.testOk")}</span>
                )}
              </div>
            ))}
            {testResult && !testResult.ok && (
              <div className="error-banner">{t("settings.escalation.testFailed", { error: testResult.error })}</div>
            )}
          </div>
        ) : (
          "—"
        )}
      </td>
      <td>
        <div className="row-actions" style={{ justifyContent: "flex-end" }}>
          {removeError && <div className="error-banner">{removeError}</div>}

          <button className="btn btn-sm" onClick={onEdit}>
            {policy ? t("common.edit") : t("settings.escalation.configure")}
          </button>

          {policy &&
            (confirmingRemove ? (
              <>
                <span className="helper-text">{t("settings.escalation.removeConfirm")}</span>
                <button className="btn btn-danger btn-sm" onClick={remove} disabled={removing}>
                  {removing ? t("common.saving") : t("common.confirm")}
                </button>
                <button className="btn btn-ghost btn-sm" onClick={cancel}>
                  {t("common.cancel")}
                </button>
              </>
            ) : (
              <button className="btn btn-danger btn-sm" onClick={() => confirm()} disabled={removing}>
                {t("common.remove")}
              </button>
            ))}
        </div>
      </td>
    </tr>
  );
}
