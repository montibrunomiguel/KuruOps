import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import { ESCALATION_WEBHOOK_PLACEHOLDERS, type EscalationChannelType, type EscalationPolicy, type EscalationStep, type SaveEscalationStepRequest } from "../../../types/api";
import type { Severity } from "../../../types/alerts";
import type { OnCallSchedule } from "../../../types/onCallSchedule";

const CHANNELS: EscalationChannelType[] = ["pagerduty", "slack", "webhook"];

// EditableStep drops EscalationStep's id/policyId/position -- a step being
// edited (including every brand-new one added via "+ Add Step") has no
// stable id yet, since EscalationPolicyRepository.replaceSteps always
// deletes-and-reinserts on save. destination starts blank (a secret, never
// returned by the API); "" on save means keep that position's existing
// secret (see EscalationPolicyService.Save), which is only valid for a
// position that already had a saved step there.
//
// key is purely a client-side React list key (crypto.randomUUID(), stable
// for this step's lifetime in the form regardless of moveStep/removeStep
// reordering the array around it) -- never sent to the backend, see save()
// below, which builds SaveEscalationStepRequest explicitly field-by-field
// rather than spreading this whole object.
interface EditableStep {
  key: string;
  scheduleId: string;
  delayMinutes: string;
  channelType: EscalationChannelType;
  destination: string;
  webhookPayloadTemplate: string;
  hadSavedSecret: boolean;
}

function toEditableStep(step: EscalationStep): EditableStep {
  return {
    key: crypto.randomUUID(),
    scheduleId: step.scheduleId,
    delayMinutes: String(step.delayMinutes),
    channelType: step.channelType,
    destination: "",
    webhookPayloadTemplate: step.webhookPayloadTemplate ?? "",
    hadSavedSecret: true,
  };
}

function emptyStep(defaultScheduleId: string): EditableStep {
  return { key: crypto.randomUUID(), scheduleId: defaultScheduleId, delayMinutes: "15", channelType: "webhook", destination: "", webhookPayloadTemplate: "", hadSavedSecret: false };
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

export function EscalationEditForm({
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
        <div key={step.key} style={{ border: "1px solid var(--border)", borderRadius: 8, padding: 10, marginBottom: 10 }}>
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
