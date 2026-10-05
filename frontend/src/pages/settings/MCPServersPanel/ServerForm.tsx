import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { MCPTransport } from "../../../types/api";
import { AuthFields, EMPTY_AUTH_DRAFT, authPayload, type AuthDraft } from "./AuthFields";

function parseCsv(v: string): string[] {
  return v
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

export function ServerForm({ onCancel, onSaved }: { onCancel: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState("");
  const [transport, setTransport] = useState<MCPTransport>("http");
  const [endpointOrCommand, setEndpointOrCommand] = useState("");
  const [auth, setAuth] = useState<AuthDraft>(EMPTY_AUTH_DRAFT);
  const [allowedTools, setAllowedTools] = useState("");
  const [sideEffectingTools, setSideEffectingTools] = useState("");
  const [enabledForAlerts, setEnabledForAlerts] = useState(true);
  const [enabledForIncidents, setEnabledForIncidents] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);

    const allowed = parseCsv(allowedTools);
    const sideEffecting = parseCsv(sideEffectingTools);
    const notAllowed = sideEffecting.filter((tool) => !allowed.includes(tool));
    if (notAllowed.length > 0) {
      setError(`${t("settings.mcp.form.sideEffectingTools")} ${t("settings.mcp.form.requiresApproval")}: ${notAllowed.join(", ")}`);
      return;
    }

    const enabledFor = [
      ...(enabledForAlerts ? ["alert_analysis"] : []),
      ...(enabledForIncidents ? ["incident_analysis"] : []),
    ];

    setSubmitting(true);
    try {
      await api.post(
        "/api/v1/settings/mcp-servers",
        {
          name,
          transport,
          endpointOrCommand,
          ...authPayload(auth),
          allowedTools: allowed,
          sideEffectingTools: sideEffecting,
          enabledFor,
        },
        token,
      );
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 14 }}>
      {error && <div className="error-banner">{error}</div>}

      <div className="form-grid">
        <div className="field">
          <label htmlFor="mcp-name">{t("settings.mcp.form.name")}</label>
          <input id="mcp-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="mcp-transport">{t("settings.mcp.form.transport")}</label>
          <select
            id="mcp-transport"
            className="select"
            value={transport}
            onChange={(e) => setTransport(e.target.value as MCPTransport)}
          >
            <option value="http">HTTP</option>
            <option value="sse">SSE</option>
            <option value="stdio">stdio</option>
          </select>
        </div>
        <div className="field field-full">
          <label htmlFor="mcp-endpoint">
            {transport === "stdio" ? t("settings.mcp.form.command") : t("settings.mcp.form.endpointUrl")}
          </label>
          <input
            id="mcp-endpoint"
            className="input"
            placeholder={transport === "stdio" ? t("settings.mcp.form.commandPlaceholder") : t("settings.mcp.form.endpointPlaceholder")}
            value={endpointOrCommand}
            onChange={(e) => setEndpointOrCommand(e.target.value)}
            required
          />
        </div>
        <AuthFields value={auth} onChange={setAuth} />
      </div>

      <hr className="section-divider" />

      <p className="helper-text" style={{ marginBottom: 10 }}>
        {t("settings.mcp.form.discoverHelper")}
        <code>tools/list</code>.
      </p>

      <div className="field">
        <label htmlFor="mcp-allowed">
          {t("settings.mcp.form.allowedTools")} <span className="field-hint">{t("settings.mcp.form.commaSeparated")}</span>
        </label>
        <input
          id="mcp-allowed"
          className="input"
          placeholder={t("settings.mcp.form.allowedToolsPlaceholder")}
          value={allowedTools}
          onChange={(e) => setAllowedTools(e.target.value)}
        />
        <span className="field-hint">{t("settings.mcp.form.allowedToolsHint")}</span>
      </div>

      <div className="field">
        <label htmlFor="mcp-sideeffect">
          {t("settings.mcp.form.sideEffectingTools")} <span className="field-hint">{t("settings.mcp.form.requiresApproval")}</span>
        </label>
        <input
          id="mcp-sideeffect"
          className="input"
          placeholder={t("settings.mcp.form.sideEffectingPlaceholder")}
          value={sideEffectingTools}
          onChange={(e) => setSideEffectingTools(e.target.value)}
        />
        <span className="field-hint">{t("settings.mcp.form.sideEffectingHint")}</span>
      </div>

      <div className="field">
        <label>{t("settings.mcp.form.enabledFor")}</label>
        <div style={{ display: "flex", gap: 16 }}>
          <label className="checkbox-row">
            <input type="checkbox" checked={enabledForAlerts} onChange={(e) => setEnabledForAlerts(e.target.checked)} />
            {t("settings.mcp.form.alertAnalysis")}
          </label>
          <label className="checkbox-row">
            <input
              type="checkbox"
              checked={enabledForIncidents}
              onChange={(e) => setEnabledForIncidents(e.target.checked)}
            />
            {t("settings.mcp.form.incidentAnalysis")}
          </label>
        </div>
      </div>

      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}
