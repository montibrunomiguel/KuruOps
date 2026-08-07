import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { EscalationChannelType, EscalationPolicy } from "../../types/api";
import type { Severity } from "../../types/alerts";

const SEVERITIES: Severity[] = ["critical", "high", "medium", "low", "informational"];
const CHANNELS: EscalationChannelType[] = ["pagerduty", "slack", "webhook"];

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

// Settings -> On-Call Escalation: one row per severity, opt-in (no row
// unless "Configure" is clicked) -- an alert of that severity left 'open'
// past the configured threshold gets a notification fired by cmd/worker's
// escalation sweep (see EscalationPolicyHandlers on the backend).
export function EscalationPoliciesPanel() {
  const { t } = useTranslation();
  const { data: policies, loading, error, reload } = useList<EscalationPolicy>((tk) =>
    api.get<EscalationPolicy[]>("/api/v1/settings/escalation-policies", tk),
  );

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
      {!loading &&
        SEVERITIES.map((severity) => (
          <EscalationRow
            key={severity}
            severity={severity}
            policy={policies?.find((p) => p.severity === severity) ?? null}
            onChanged={reload}
          />
        ))}
    </div>
  );
}

function EscalationRow({
  severity,
  policy,
  onChanged,
}: {
  severity: Severity;
  policy: EscalationPolicy | null;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [configuring, setConfiguring] = useState(policy !== null);
  const [minutes, setMinutes] = useState(policy ? String(policy.unacknowledgedAfterMinutes) : "15");
  const [channelType, setChannelType] = useState<EscalationChannelType>(policy?.channelType ?? "webhook");
  const [destination, setDestination] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);

  async function save() {
    setSaving(true);
    setError(null);
    try {
      await api.put(
        "/api/v1/settings/escalation-policies",
        { severity, unacknowledgedAfterMinutes: Number(minutes), channelType, destination },
        token,
      );
      setDestination("");
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function remove() {
    if (!policy) return;
    setSaving(true);
    setError(null);
    try {
      await api.del(`/api/v1/settings/escalation-policies/${policy.id}`, token);
      setConfirming(false);
      setConfiguring(false);
      setDestination("");
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  if (!configuring) {
    return (
      <div className="row">
        <div className="row-main">
          <strong>{t(`common.severity.${severity}`)}</strong>{" "}
          <span className="field-hint">{t("settings.escalation.notConfigured")}</span>
        </div>
        <div className="row-actions">
          <button className="btn btn-sm" onClick={() => setConfiguring(true)}>
            {t("settings.escalation.configure")}
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="row" style={{ alignItems: "flex-start", flexWrap: "wrap" }}>
      <div className="row-main" style={{ minWidth: 120 }}>
        <strong>{t(`common.severity.${severity}`)}</strong>
        {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
      </div>

      <div style={{ display: "flex", gap: 10, flexWrap: "wrap", alignItems: "flex-end" }}>
        <div className="field">
          <label htmlFor={`esc-min-${severity}`}>{t("settings.escalation.form.minutes")}</label>
          <input
            id={`esc-min-${severity}`}
            className="input"
            type="number"
            min={1}
            style={{ width: 90 }}
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
        <div className="field">
          <label htmlFor={`esc-dest-${severity}`}>
            {t("settings.escalation.form.destination")}{" "}
            {policy && <span className="field-hint">{t("settings.escalation.form.destinationHint")}</span>}
          </label>
          <input
            id={`esc-dest-${severity}`}
            className="input"
            type="password"
            style={{ width: 240 }}
            placeholder={destinationPlaceholder(channelType)}
            value={destination}
            onChange={(e) => setDestination(e.target.value)}
          />
        </div>

        <button
          className="btn btn-primary btn-sm"
          onClick={save}
          disabled={saving || (!policy && !destination)}
        >
          {saving ? t("common.saving") : t("common.save")}
        </button>

        {!policy && (
          <button className="btn btn-ghost btn-sm" onClick={() => setConfiguring(false)} disabled={saving}>
            {t("common.cancel")}
          </button>
        )}

        {policy &&
          (confirming ? (
            <>
              <span className="helper-text">{t("settings.escalation.removeConfirm")}</span>
              <button className="btn btn-danger btn-sm" onClick={remove} disabled={saving}>
                {saving ? t("common.saving") : t("common.confirm")}
              </button>
              <button className="btn btn-ghost btn-sm" onClick={() => setConfirming(false)}>
                {t("common.cancel")}
              </button>
            </>
          ) : (
            <button className="btn btn-danger btn-sm" onClick={() => setConfirming(true)} disabled={saving}>
              {t("common.remove")}
            </button>
          ))}
      </div>
    </div>
  );
}
