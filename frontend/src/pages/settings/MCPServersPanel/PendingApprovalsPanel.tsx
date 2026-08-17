import { useTranslation } from "react-i18next";
import { api } from "../../../api/client";
import { useList } from "../../../api/hooks";
import type { AIToolCall, MCPServer } from "../../../types/api";
import { PendingApprovalRow } from "./PendingApprovalRow";

// PendingApprovalsPanel lists every side-effecting MCP tool call an
// "Analyze with AI" agentic run has proposed and paused on (see
// AIAnalysisService's agentic loop on the backend) -- approving lets the
// paused analysis resume with the tool's real result; rejecting resumes it
// without one. servers is passed down (rather than each row fetching its
// own server) just to resolve a friendly server name next to each call.
export function PendingApprovalsPanel({ servers }: { servers: MCPServer[] }) {
  const { t } = useTranslation();
  const { data: calls, loading, error, reload } = useList<AIToolCall>(["mcp-pending-tool-calls"], (tk) =>
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
