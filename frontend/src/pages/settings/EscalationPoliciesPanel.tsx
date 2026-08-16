import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import { ESCALATION_WEBHOOK_PLACEHOLDERS, type EscalationChannelType, type EscalationPolicy, type EscalationStep, type SaveEscalationStepRequest } from "../../types/api";
import type { Severity } from "../../types/alerts";
import type { OnCallSchedule } from "../../types/onCallSchedule";

const SEVERITIES: Severity[] = ["critical", "high", "medium", "low", "informational"];
const CHANNELS: EscalationChannelType[] = ["pagerduty", "slack", "webhook"];

// EditableStep drops EscalationStep's id/policyId/position -- a step being
// edited (including every brand-new one added via "+ Add Step") has no
// stable id yet, since EscalationPolicyRepository.replaceSteps always
// deletes-and-reinserts on save. destination starts blank (a secret, never
// returned by the API); "" on save means keep that position's existing
// secret (see EscalationPolicyService.Save), which is only valid for a
// position that already had a saved step there.
interface EditableStep {
  scheduleId: string;
  delayMinutes: string;
  channelType: EscalationChannelType;
  destination: string;
  webhookPayloadTemplate: string;
  hadSavedSecret: boolean;
}

function toEditableStep(step: EscalationStep): EditableStep {
  return {
    scheduleId: step.scheduleId,
    delayMinutes: String(step.delayMinutes),
    channelType: step.channelType,
    destination: "",
    webhookPayloadTemplate: step.webhookPayloadTemplate ?? "",
    hadSavedSecret: true,
  };
}

function emptyStep(defaultScheduleId: string): EditableStep {
  return { scheduleId: defaultScheduleId, delayMinutes: "15", channelType: "webhook", destination: "", webhookPayloadTemplate: "", hadSavedSecret: false };
}

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

function EscalationRow({
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
  const [confirmingRemove, setConfirmingRemove] = useState(false);
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
      setConfirmingRemove(false);
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
                <button className="btn btn-ghost btn-sm" onClick={() => setConfirmingRemove(false)}>
                  {t("common.cancel")}
                </button>
              </>
            ) : (
              <button className="btn btn-danger btn-sm" onClick={() => setConfirmingRemove(true)} disabled={removing}>
                {t("common.remove")}
              </button>
            ))}
        </div>
      </td>
    </tr>
  );
}

function destinationPlaceholder(channelType: EscalationChannelType): string {
  switch (channelType) {
    case "pagerduty":
      return "Integration/Routing Key";
    case "slack":
      return "https://hooks.slack.com/services/...";
    case "webhook":
      return "https://...";
  }
}

function EscalationEditForm({
  severity,
  policy,
  schedules,
  onCancel,
  onSaved,
}: {
  severity: Severity;
  policy: EscalationPolicy | null;
  schedules: OnCallSchedule[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [steps, setSteps] = useState<EditableStep[]>(
    policy && policy.steps.length > 0 ? policy.steps.map(toEditableStep) : [emptyStep(schedules[0]?.id ?? "")],
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function updateStep(idx: number, patch: Partial<EditableStep>) {
    setSteps((s) => s.map((step, i) => (i === idx ? { ...step, ...patch } : step)));
  }

  function addStep() {
    setSteps((s) => [...s, emptyStep(schedules[0]?.id ?? "")]);
  }

  function removeStep(idx: number) {
    setSteps((s) => s.filter((_, i) => i !== idx));
  }

  function moveStep(idx: number, direction: -1 | 1) {
    const target = idx + direction;
    if (target < 0 || target >= steps.length) return;
    setSteps((s) => {
      const next = [...s];
      const [moved] = next.splice(idx, 1);
      next.splice(target, 0, moved);
      return next;
    });
  }

  async function save() {
    setSaving(true);
    setError(null);
    try {
      const body: { severity: Severity; steps: SaveEscalationStepRequest[] } = {
        severity,
        steps: steps.map((step) => ({
          scheduleId: step.scheduleId,
          delayMinutes: Number(step.delayMinutes),
          channelType: step.channelType,
          destination: step.destination,
          webhookPayloadTemplate: step.channelType === "webhook" ? step.webhookPayloadTemplate : "",
        })),
      };
      await api.put("/api/v1/settings/escalation-policies", body, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  const canSave = steps.length > 0 && steps.every((s) => s.scheduleId && s.delayMinutes && (s.hadSavedSecret || s.destination));

  return (
    <div style={{ padding: "10px 0" }}>
      {error && <div className="error-banner" style={{ marginBottom: 10 }}>{error}</div>}

      {steps.map((step, idx) => (
        <div key={idx} style={{ border: "1px solid var(--border)", borderRadius: 8, padding: 10, marginBottom: 10 }}>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 8 }}>
            <strong style={{ fontSize: 12.5 }}>{t("settings.escalation.form.step", { num: idx + 1 })}</strong>
            <div className="row-actions">
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => moveStep(idx, -1)}
                disabled={idx === 0}
                aria-label={t("settings.escalation.form.moveUp")}
              >
                ↑
              </button>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => moveStep(idx, 1)}
                disabled={idx === steps.length - 1}
                aria-label={t("settings.escalation.form.moveDown")}
              >
                ↓
              </button>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => removeStep(idx)}>
                {t("settings.escalation.form.removeStep")}
              </button>
            </div>
          </div>

          <div className="form-grid">
            <div className="field">
              <label htmlFor={`esc-sched-${severity}-${idx}`}>{t("settings.escalation.form.schedule")}</label>
              <select
                id={`esc-sched-${severity}-${idx}`}
                className="select"
                value={step.scheduleId}
                onChange={(e) => updateStep(idx, { scheduleId: e.target.value })}
              >
                {schedules.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label htmlFor={`esc-min-${severity}-${idx}`}>{t("settings.escalation.form.delayMinutes")}</label>
              <input
                id={`esc-min-${severity}-${idx}`}
                className="input"
                type="number"
                min={1}
                value={step.delayMinutes}
                onChange={(e) => updateStep(idx, { delayMinutes: e.target.value })}
                // select-on-focus: emptyStep() pre-fills "15", so typing
                // without clearing first would append instead of replace.
                onFocus={(e) => e.target.select()}
              />
            </div>
            <div className="field">
              <label htmlFor={`esc-channel-${severity}-${idx}`}>{t("settings.escalation.form.channel")}</label>
              <select
                id={`esc-channel-${severity}-${idx}`}
                className="select"
                value={step.channelType}
                onChange={(e) => updateStep(idx, { channelType: e.target.value as EscalationChannelType })}
              >
                {CHANNELS.map((c) => (
                  <option key={c} value={c}>
                    {t(`settings.escalation.form.channelOption.${c}`)}
                  </option>
                ))}
              </select>
            </div>
            <div className="field field-full">
              <label htmlFor={`esc-dest-${severity}-${idx}`}>
                {t(`settings.escalation.form.destinationLabel.${step.channelType}`)}{" "}
                {step.hadSavedSecret && <span className="field-hint">{t("settings.escalation.form.destinationHint")}</span>}
              </label>
              <input
                id={`esc-dest-${severity}-${idx}`}
                className="input"
                type="password"
                placeholder={destinationPlaceholder(step.channelType)}
                value={step.destination}
                onChange={(e) => updateStep(idx, { destination: e.target.value })}
              />
              <span className="field-hint">{t(`settings.escalation.form.destinationHelp.${step.channelType}`)}</span>
            </div>

            {step.channelType === "webhook" && (
              <div className="field field-full">
                <label htmlFor={`esc-payload-${severity}-${idx}`}>{t("settings.escalation.form.webhookPayloadTemplate")}</label>
                <textarea
                  id={`esc-payload-${severity}-${idx}`}
                  className="textarea mono"
                  style={{ minHeight: 80 }}
                  placeholder={'{"text": "{{severity}}: {{title}} -- {{analystName}}"}'}
                  value={step.webhookPayloadTemplate}
                  onChange={(e) => updateStep(idx, { webhookPayloadTemplate: e.target.value })}
                />
                <span className="field-hint">
                  {t("settings.escalation.form.webhookPayloadTemplateHelp")} {ESCALATION_WEBHOOK_PLACEHOLDERS.join(", ")}
                </span>
              </div>
            )}
          </div>
        </div>
      ))}

      <div className="row-actions" style={{ marginBottom: 12 }}>
        <button type="button" className="btn btn-sm" onClick={addStep}>
          {t("settings.escalation.form.addStep")}
        </button>
      </div>

      <div className="row-actions">
        <button className="btn btn-primary btn-sm" onClick={save} disabled={saving || !canSave}>
          {saving ? t("common.saving") : t("common.save")}
        </button>
        <button className="btn btn-ghost btn-sm" onClick={onCancel} disabled={saving}>
          {t("common.cancel")}
        </button>
      </div>
    </div>
  );
}
