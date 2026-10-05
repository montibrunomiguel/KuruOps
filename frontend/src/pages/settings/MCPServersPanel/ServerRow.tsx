import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { MCPServer } from "../../../types/api";
import { DiscoverToolsPanel } from "./DiscoverToolsPanel";
import { AuthEditPanel } from "./AuthEditPanel";

export function ServerRow({
  server,
  onChanged,
  confirming,
  deleting,
  onConfirm,
  onCancel,
  onRemove,
}: {
  server: MCPServer;
  onChanged: () => void;
  confirming: boolean;
  deleting: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [busy, setBusy] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [editingAuth, setEditingAuth] = useState(false);
  const [error, setError] = useState<string | null>(null);

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
          <p className="row-sub">
            {t("settings.mcp.authBadge", { type: t(`settings.mcp.auth.types.${server.authType}`) })}
            {server.authType === "api_key" && server.authHeaderName && ` · ${server.authHeaderName}`}
            {server.authType === "oauth" && server.oauthClientId && ` · ${server.oauthClientId}`}
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
          <button className="btn btn-sm" onClick={() => setEditingAuth((v) => !v)}>
            {editingAuth ? t("common.close") : t("settings.mcp.editAuth")}
          </button>
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
              <button className="btn btn-danger btn-sm" onClick={onRemove} disabled={deleting}>
                {deleting ? t("common.saving") : t("common.confirmDelete")}
              </button>
              <button className="btn btn-ghost btn-sm" onClick={onCancel}>
                {t("common.cancel")}
              </button>
            </>
          ) : (
            <button className="btn btn-danger btn-sm" onClick={onConfirm} disabled={deleting}>
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

      {editingAuth && (
        <AuthEditPanel
          server={server}
          onClose={() => setEditingAuth(false)}
          onSaved={() => {
            setEditingAuth(false);
            onChanged();
          }}
        />
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
