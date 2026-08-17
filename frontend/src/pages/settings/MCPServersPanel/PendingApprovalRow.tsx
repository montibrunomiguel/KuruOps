import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { AIToolCall } from "../../../types/api";

export function PendingApprovalRow({
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
