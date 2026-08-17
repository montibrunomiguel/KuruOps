import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, useObject, mutationErrorMessage } from "../../api/hooks";
import type { AIToolCall, DiscoveredTool, MCPServer, MCPTransport } from "../../types/api";

function parseCsv(v: string): string[] {
  return v
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

export function MCPServersPanel() {
  const { t } = useTranslation();
  const { data: servers, loading, error, reload } = useList<MCPServer>((tk) =>
    api.get<MCPServer[]>("/api/v1/settings/mcp-servers", tk),
  );
  const [showCreate, setShowCreate] = useState(false);

  return (
    <>
      <PendingApprovalsPanel servers={servers ?? []} />

      <div className="panel">
        <div className="panel-header">
          <h2 className="panel-title">{t("settings.mcp.title")}</h2>
          <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
            {t("settings.mcp.newServer")}
          </button>
        </div>
        <p className="helper-text" style={{ marginBottom: 14 }}>
          {t("settings.mcp.helperTextPre")}
          <strong>{t("settings.mcp.helperTextMid")}</strong>
          {t("settings.mcp.helperTextPost")}
        </p>

        {error && <div className="error-banner">{error}</div>}

        {showCreate && (
          <ServerForm
            onCancel={() => setShowCreate(false)}
            onSaved={() => {
              setShowCreate(false);
              reload();
            }}
          />
        )}

        {loading && <div className="empty-state">{t("common.loading")}</div>}
        {!loading && servers && servers.length === 0 && (
          <div className="empty-state">{t("settings.mcp.noServers")}</div>
        )}
        {!loading && servers && servers.map((s) => <ServerRow key={s.id} server={s} onChanged={reload} />)}
      </div>
    </>
  );
}

// PendingApprovalsPanel lists every side-effecting MCP tool call an
// "Analyze with AI" agentic run has proposed and paused on (see
// AIAnalysisService's agentic loop on the backend) -- approving lets the
// paused analysis resume with the tool's real result; rejecting resumes it
// without one. servers is passed down (rather than each row fetching its
// own server) just to resolve a friendly server name next to each call.
function PendingApprovalsPanel({ servers }: { servers: MCPServer[] }) {
  const { t } = useTranslation();
  const { data: calls, loading, error, reload } = useList<AIToolCall>((tk) =>
    api.get<AIToolCall[]>("/api/v1/settings/mcp-servers/tool-calls", tk),
  );

  if (loading || (calls && calls.length === 0)) return null;

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.mcp.approvals.title")}</h2>
      </div>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.mcp.approvals.helper")}
      </p>
      {error && <div className="error-banner">{error}</div>}
      {calls &&
        calls.map((call) => (
          <PendingApprovalRow
            key={call.id}
            call={call}
            serverName={servers.find((s) => s.id === call.mcpServerId)?.name ?? call.mcpServerId}
            onChanged={reload}
          />
        ))}
    </div>
  );
}

function PendingApprovalRow({
  call,
  serverName,
  onChanged,
}: {
  call: AIToolCall;
  serverName: string;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);

  async function decide(action: "approve" | "reject") {
    setBusy(true);
    setError(null);
    try {
      await api.post(`/api/v1/settings/mcp-servers/tool-calls/${call.id}/${action}`, {}, token);
      setConfirming(false);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="row" style={{ alignItems: "flex-start", flexWrap: "wrap" }}>
      <div className="row-main">
        <p className="row-title">
          {call.toolName} <span className="badge badge-muted">{serverName}</span>
        </p>
        <p className="row-sub">
          {t(`settings.mcp.approvals.contextType.${call.contextType}`)} · {call.contextId}
        </p>
        {Object.keys(call.args ?? {}).length > 0 && (
          <pre className="mono" style={{ margin: "6px 0 0", fontSize: 12, background: "var(--surface-2)", padding: 8, borderRadius: 6, overflowX: "auto" }}>
            {JSON.stringify(call.args, null, 2)}
          </pre>
        )}
        {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
      </div>
      <div className="row-actions">
        {confirming ? (
          <>
            <span className="helper-text" style={{ flexBasis: "100%" }}>
              {t("settings.mcp.approvals.approveConfirm", { tool: call.toolName })}
            </span>
            <button className="btn btn-primary btn-sm" onClick={() => decide("approve")} disabled={busy}>
              {busy ? t("common.saving") : t("common.confirm")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setConfirming(false)}>
              {t("common.cancel")}
            </button>
          </>
        ) : (
          <>
            <button className="btn btn-primary btn-sm" onClick={() => setConfirming(true)} disabled={busy}>
              {t("settings.mcp.approvals.approve")}
            </button>
            <button className="btn btn-danger btn-sm" onClick={() => decide("reject")} disabled={busy}>
              {t("settings.mcp.approvals.reject")}
            </button>
          </>
        )}
      </div>
    </div>
  );
}

function ServerForm({ onCancel, onSaved }: { onCancel: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState("");
  const [transport, setTransport] = useState<MCPTransport>("http");
  const [endpointOrCommand, setEndpointOrCommand] = useState("");
  const [authToken, setAuthToken] = useState("");
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
          authToken: authToken || undefined,
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
        <div className="field field-full">
          <label htmlFor="mcp-auth">
            {t("settings.mcp.form.authToken")} <span className="field-hint">{t("settings.mcp.form.optional")}</span>
          </label>
          <input
            id="mcp-auth"
            className="input"
            type="password"
            value={authToken}
            onChange={(e) => setAuthToken(e.target.value)}
          />
        </div>
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

function ServerRow({ server, onChanged }: { server: MCPServer; onChanged: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [busy, setBusy] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);

  async function toggle() {
    setBusy(true);
    setError(null);
    try {
      const action = server.isEnabled ? "disable" : "enable";
      await api.post(`/api/v1/settings/mcp-servers/${server.id}/${action}`, {}, token);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setBusy(true);
    setError(null);
    try {
      await api.del(`/api/v1/settings/mcp-servers/${server.id}`, token);
      setConfirming(false);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="row" style={{ alignItems: "flex-start", flexDirection: "column" }}>
      <div style={{ display: "flex", width: "100%", alignItems: "flex-start" }}>
        <div className="row-main">
          <p className="row-title">
            {server.name}{" "}
            <span className={`badge ${server.isEnabled ? "badge-success" : "badge-muted"}`}>
              <span className="badge-status-dot" />
              {server.isEnabled ? t("settings.mcp.enabledBadge") : t("settings.mcp.disabledBadge")}
            </span>
          </p>
          <p className="row-sub">
            {server.transport} · {server.endpointOrCommand}
          </p>
          <div className="tag-chip-list" style={{ marginTop: 8 }}>
            {server.allowedTools.length === 0 && (
              <span className="field-hint">{t("settings.mcp.noAllowedTools")}</span>
            )}
            {server.allowedTools.map((tool) => (
              <span key={tool} className="tag-chip">
                {tool}
                {server.sideEffectingTools.includes(tool) && (
                  <span title={t("settings.mcp.requiresApprovalTitle")} style={{ color: "var(--high)" }}>
                    ⚠
                  </span>
                )}
              </span>
            ))}
          </div>
          {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
        </div>
        <div className="row-actions">
          <button className="btn btn-sm" onClick={() => setDiscovering((v) => !v)} disabled={server.transport !== "http"}>
            {discovering ? t("common.close") : t("settings.mcp.discoverTools")}
          </button>
          <button className="btn btn-sm" onClick={toggle} disabled={busy}>
            {server.isEnabled ? t("common.disable") : t("common.enable")}
          </button>
          {confirming ? (
            <>
              <span className="helper-text" style={{ flexBasis: "100%" }}>
                {t("settings.mcp.removeConfirm", { name: server.name })}
              </span>
              <button className="btn btn-danger btn-sm" onClick={remove} disabled={busy}>
                {busy ? t("common.saving") : t("common.confirmDelete")}
              </button>
              <button className="btn btn-ghost btn-sm" onClick={() => setConfirming(false)}>
                {t("common.cancel")}
              </button>
            </>
          ) : (
            <button className="btn btn-danger btn-sm" onClick={() => setConfirming(true)} disabled={busy}>
              {t("common.remove")}
            </button>
          )}
        </div>
      </div>

      {server.transport !== "http" && discovering === false && (
        <p className="field-hint" style={{ marginTop: 4 }}>
          {t("settings.mcp.discoverHttpOnly")}
        </p>
      )}

      {discovering && (
        <DiscoverToolsPanel
          server={server}
          onClose={() => setDiscovering(false)}
          onSaved={() => {
            setDiscovering(false);
            onChanged();
          }}
        />
      )}
    </div>
  );
}

function DiscoverToolsPanel({
  server,
  onClose,
  onSaved,
}: {
  server: MCPServer;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: tools, loading, error } = useObject<DiscoveredTool[]>(
    (tok) => api.post<DiscoveredTool[]>(`/api/v1/settings/mcp-servers/${server.id}/discover-tools`, {}, tok),
    [server.id],
  );
  const [allowed, setAllowed] = useState<Set<string>>(new Set(server.allowedTools));
  const [sideEffecting, setSideEffecting] = useState<Set<string>>(new Set(server.sideEffectingTools));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  function toggleAllowed(name: string) {
    setAllowed((prev) => {
      const next = new Set(prev);
      if (next.has(name)) {
        next.delete(name);
        setSideEffecting((se) => {
          const s = new Set(se);
          s.delete(name);
          return s;
        });
      } else {
        next.add(name);
      }
      return next;
    });
  }

  function toggleSideEffecting(name: string) {
    setSideEffecting((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }

  async function save() {
    setSaving(true);
    setSaveError(null);
    try {
      await api.put(
        `/api/v1/settings/mcp-servers/${server.id}`,
        {
          name: server.name,
          transport: server.transport,
          endpointOrCommand: server.endpointOrCommand,
          allowedTools: Array.from(allowed),
          sideEffectingTools: Array.from(sideEffecting),
          enabledFor: server.enabledFor,
        },
        token,
      );
      onSaved();
    } catch (err) {
      setSaveError(mutationErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="panel" style={{ width: "100%", marginTop: 10, background: "var(--surface-2)" }}>
      {error && <div className="error-banner">{error}</div>}
      {saveError && <div className="error-banner">{saveError}</div>}
      {loading && <div className="empty-state">{t("settings.mcp.discover.querying")}</div>}
      {!loading && tools && tools.length === 0 && (
        <div className="empty-state">{t("settings.mcp.discover.noTools")}</div>
      )}
      {!loading && tools && tools.length > 0 && (
        <>
          <p className="field-hint" style={{ marginBottom: 10 }}>
            {t("settings.mcp.discover.helper")}
          </p>
          {tools.map((tool) => (
            <div key={tool.name} className="row" style={{ padding: "8px 0" }}>
              <div className="row-main">
                <label className="checkbox-row">
                  <input type="checkbox" checked={allowed.has(tool.name)} onChange={() => toggleAllowed(tool.name)} />
                  <strong>{tool.name}</strong>
                </label>
                {tool.description && (
                  <p className="row-sub" style={{ marginLeft: 22 }}>
                    {tool.description}
                  </p>
                )}
              </div>
              {allowed.has(tool.name) && (
                <label className="checkbox-row">
                  <input
                    type="checkbox"
                    checked={sideEffecting.has(tool.name)}
                    onChange={() => toggleSideEffecting(tool.name)}
                  />
                  {t("settings.mcp.discover.sideEffecting")}
                </label>
              )}
            </div>
          ))}
          <div className="row-actions" style={{ marginTop: 10 }}>
            <button className="btn btn-primary btn-sm" onClick={save} disabled={saving}>
              {saving ? t("common.saving") : t("settings.mcp.discover.saveAllowList")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={onClose}>
              {t("common.cancel")}
            </button>
          </div>
        </>
      )}
    </div>
  );
}
