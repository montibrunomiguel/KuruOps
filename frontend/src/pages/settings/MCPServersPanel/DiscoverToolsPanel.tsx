import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { useObject, mutationErrorMessage } from "../../../api/hooks";
import type { DiscoveredTool, MCPServer } from "../../../types/api";

export function DiscoverToolsPanel({
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
