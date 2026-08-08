import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import { WEBHOOK_PAYLOAD_PLACEHOLDERS, type EscalationChannelType, type EscalationPolicy } from "../../types/api";
import type { Severity } from "../../types/alerts";

const SEVERITIES: Severity[] = ["critical", "high", "medium", "low", "informational"];
const CHANNELS: EscalationChannelType[] = ["pagerduty", "slack", "webhook"];

// Settings -> On-Call Escalation: an overview table (one row per severity)
// instead of five permanently-expanded forms -- Configured/Not configured
// at a glance, channel and threshold visible without opening anything.
// Editing a row expands it in place; a saved row also gets a "Send test"
// action (mirrors SMTPConfigPanel's "Send test email") so an admin can
// confirm delivery -- and, for webhook, a custom payload template -- works
// before waiting on a real unacknowledged alert.
export function EscalationPoliciesPanel() {
  const { t } = useTranslation();
  const { data: policies, loading, error, reload } = useList<EscalationPolicy>((tk) =>
    api.get<EscalationPolicy[]>("/api/v1/settings/escalation-policies", tk),
  );
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

      {!loading && (
        <table className="table">
          <thead>
            <tr>
              <th>{t("settings.escalation.columns.severity")}</th>
              <th>{t("settings.escalation.columns.status")}</th>
              <th>{t("settings.escalation.columns.channel")}</th>
              <th>{t("settings.escalation.columns.minutes")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {SEVERITIES.map((severity) => (
              <EscalationRow
                key={severity}
                severity={severity}
                policy={policies?.find((p) => p.severity === severity) ?? null}
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
  editing,
  onEdit,
  onCancelEdit,
  onChanged,
}: {
  severity: Severity;
  policy: EscalationPolicy | null;
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
  const [testState, setTestState] = useState<"idle" | "sending" | "ok" | "error">("idle");
  const [testError, setTestError] = useState<string | null>(null);

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

  async function sendTest() {
    setTestState("sending");
    setTestError(null);
    try {
      await api.post("/api/v1/settings/escalation-policies/test", { severity }, token);
      setTestState("ok");
    } catch (err) {
      setTestState("error");
      setTestError(mutationErrorMessage(err));
    }
  }

  if (editing) {
    return (
      <tr>
        <td colSpan={5}>
          <EscalationEditForm severity={severity} policy={policy} onCancel={onCancelEdit} onSaved={onChanged} />
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
      <td>{policy ? t(`settings.escalation.form.channelOption.${policy.channelType}`) : "—"}</td>
      <td>{policy ? policy.unacknowledgedAfterMinutes : "—"}</td>
      <td>
        <div className="row-actions" style={{ justifyContent: "flex-end" }}>
          {removeError && <div className="error-banner">{removeError}</div>}
          {testState === "ok" && <span className="helper-text" style={{ color: "var(--success)" }}>{t("settings.escalation.testOk")}</span>}
          {testState === "error" && <div className="error-banner">{t("settings.escalation.testFailed", { error: testError })}</div>}

          {policy && (
            <button className="btn btn-ghost btn-sm" onClick={sendTest} disabled={testState === "sending"}>
              {testState === "sending" ? t("settings.escalation.testing") : t("settings.escalation.test")}
            </button>
          )}

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
  onCancel,
  onSaved,
}: {
  severity: Severity;
  policy: EscalationPolicy | null;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [minutes, setMinutes] = useState(policy ? String(policy.unacknowledgedAfterMinutes) : "15");
  const [channelType, setChannelType] = useState<EscalationChannelType>(policy?.channelType ?? "webhook");
  const [destination, setDestination] = useState("");
  // Unlike destination (a secret, always blank on load), the payload
  // template isn't sensitive -- pre-filling it lets an admin tweak an
  // existing template instead of retyping it from scratch.
  const [webhookPayloadTemplate, setWebhookPayloadTemplate] = useState(policy?.webhookPayloadTemplate ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    setSaving(true);
    setError(null);
    try {
      await api.put(
        "/api/v1/settings/escalation-policies",
        {
          severity,
          unacknowledgedAfterMinutes: Number(minutes),
          channelType,
          destination,
          webhookPayloadTemplate: channelType === "webhook" ? webhookPayloadTemplate : "",
        },
        token,
      );
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div style={{ padding: "10px 0" }}>
      {error && <div className="error-banner" style={{ marginBottom: 10 }}>{error}</div>}

      <div className="form-grid">
        <div className="field">
          <label htmlFor={`esc-min-${severity}`}>{t("settings.escalation.form.minutes")}</label>
          <input
            id={`esc-min-${severity}`}
            className="input"
            type="number"
            min={1}
            value={minutes}
            onChange={(e) => setMinutes(e.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor={`esc-channel-${severity}`}>{t("settings.escalation.form.channel")}</label>
          <select
            id={`esc-channel-${severity}`}
            className="select"
            value={channelType}
            onChange={(e) => setChannelType(e.target.value as EscalationChannelType)}
          >
            {CHANNELS.map((c) => (
              <option key={c} value={c}>
                {t(`settings.escalation.form.channelOption.${c}`)}
              </option>
            ))}
          </select>
        </div>
        <div className="field field-full">
          <label htmlFor={`esc-dest-${severity}`}>
            {t(`settings.escalation.form.destinationLabel.${channelType}`)}{" "}
            {policy && <span className="field-hint">{t("settings.escalation.form.destinationHint")}</span>}
          </label>
          <input
            id={`esc-dest-${severity}`}
            className="input"
            type="password"
            placeholder={destinationPlaceholder(channelType)}
            value={destination}
            onChange={(e) => setDestination(e.target.value)}
          />
          <span className="field-hint">{t(`settings.escalation.form.destinationHelp.${channelType}`)}</span>
        </div>

        {channelType === "webhook" && (
          <div className="field field-full">
            <label htmlFor={`esc-payload-${severity}`}>{t("settings.escalation.form.webhookPayloadTemplate")}</label>
            <textarea
              id={`esc-payload-${severity}`}
              className="textarea mono"
              style={{ minHeight: 80 }}
              placeholder={'{"text": "{{severity}}: {{title}}"}'}
              value={webhookPayloadTemplate}
              onChange={(e) => setWebhookPayloadTemplate(e.target.value)}
            />
            <span className="field-hint">
              {t("settings.escalation.form.webhookPayloadTemplateHelp")} {WEBHOOK_PAYLOAD_PLACEHOLDERS.join(", ")}
            </span>
          </div>
        )}
      </div>

      <div className="row-actions" style={{ marginTop: 12 }}>
        <button className="btn btn-primary btn-sm" onClick={save} disabled={saving || (!policy && !destination)}>
          {saving ? t("common.saving") : t("common.save")}
        </button>
        <button className="btn btn-ghost btn-sm" onClick={onCancel} disabled={saving}>
          {t("common.cancel")}
        </button>
      </div>
    </div>
  );
}
