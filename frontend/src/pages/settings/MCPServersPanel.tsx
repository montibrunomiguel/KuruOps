import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../api/client";
import { useList } from "../../api/hooks";
import type { MCPServer } from "../../types/api";
import { PendingApprovalsPanel } from "./MCPServersPanel/PendingApprovalsPanel";
import { ServerForm } from "./MCPServersPanel/ServerForm";
import { ServerRow } from "./MCPServersPanel/ServerRow";

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
