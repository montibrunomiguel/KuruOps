import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { mutationErrorMessage, useObject } from "../../api/hooks";
import { useConfirm } from "../../hooks/useConfirm";
import type { SlackConfig } from "../../types/api";

// Settings -> Conectores -> Slack: connect/disconnect a Slack workspace via
// bot-token OAuth. This is the foundation phase only -- there is no
// message/thread/channel sync UI here yet, just "is a workspace connected,
// and by whom" plus the Connect/Disconnect actions. Every future Slack
// capability (open incidents from Slack, pull thread messages into alerts,
// link an incident to a channel) stays hidden elsewhere in the app until
// this shows connected -- see backend SlackConfigService.Get's doc comment
// for the gate every one of those features checks.
//
// Same OAuth-redirect shape as StorageIntegrationPanel's Google Drive tab:
// clicking Connect navigates the whole page away to Slack's consent
// screen, which redirects back here with ?slack_connected=1 or
// ?slack_error=... -- there's no XHR request/response for that half of the
// flow, only the query-param round trip.
export function SlackIntegrationPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: existing, loading, error, reload } = useObject<SlackConfig | null>(["slack-config"], (tok) =>
    api.get<SlackConfig | null>("/api/v1/settings/integrations/slack", tok),
  );
  const [searchParams, setSearchParams] = useSearchParams();

  const [connecting, setConnecting] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);
  const [actionError, setActionError] = useState<string | null>(searchParams.get("slack_error"));
  // Read-only after mount: unlike StorageIntegrationPanel's `saved` flag
  // (which gets re-set to true on a subsequent successful form submit),
  // this panel has no such follow-up success write to `connected` -- it
  // only ever reflects the one-time query param from the OAuth redirect.
  const [connected] = useState(searchParams.get("slack_connected") === "1");
  const { confirming: confirmingRemove, confirm: confirmRemove, cancel: cancelRemove } = useConfirm();

  // Clears slack_connected/slack_error from the URL once shown, so a page
  // refresh doesn't keep re-displaying a stale result from the OAuth
  // redirect -- same pattern as StorageIntegrationPanel's gdrive_* params.
  useEffect(() => {
    if (searchParams.has("slack_connected") || searchParams.has("slack_error")) {
      setSearchParams({}, { replace: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function handleConnect() {
    setConnecting(true);
    setActionError(null);
    try {
      const { url } = await api.get<{ url: string }>("/api/v1/settings/integrations/slack/oauth/authorize-url", token);
      window.location.href = url;
    } catch (err) {
      setActionError(mutationErrorMessage(err));
      setConnecting(false);
    }
  }

  async function handleDisconnect() {
    cancelRemove();
    setDisconnecting(true);
    setActionError(null);
    try {
      await api.del("/api/v1/settings/integrations/slack", token);
      reload();
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    } finally {
      setDisconnecting(false);
    }
  }

  if (loading) return <div className="panel"><div className="empty-state">{t("common.loading")}</div></div>;

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.slack.title")}</h2>
        {existing && (
          <span className="badge badge-success">
            <span className="badge-status-dot" />
            {t("settings.slack.connectedBadge", { team: existing.teamName })}
          </span>
        )}
      </div>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.slack.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {actionError && <div className="error-banner">{actionError}</div>}
      {connected && <div className="helper-text" style={{ color: "var(--success)", marginBottom: 12 }}>{t("settings.slack.connectedMessage")}</div>}

      {existing ? (
        <>
          <div className="form-grid">
            <div className="field">
              <label>{t("settings.slack.workspace")}</label>
              <div>{existing.teamName}</div>
            </div>
            <div className="field">
              <label>{t("settings.slack.installedBy")}</label>
              <div>{existing.installedByUserName}</div>
            </div>
          </div>

          <div className="row-actions">
            {!confirmingRemove && (
              <button type="button" className="btn btn-danger btn-sm" onClick={() => confirmRemove()} disabled={disconnecting}>
                {t("settings.slack.disconnect")}
              </button>
            )}
            {confirmingRemove && (
              <>
                <span className="helper-text">{t("settings.slack.disconnectConfirm")}</span>
                <button type="button" className="btn btn-danger btn-sm" onClick={handleDisconnect} disabled={disconnecting}>
                  {disconnecting ? t("common.saving") : t("common.confirmDelete")}
                </button>
                <button type="button" className="btn btn-ghost btn-sm" onClick={() => cancelRemove()} disabled={disconnecting}>
                  {t("common.cancel")}
                </button>
              </>
            )}
          </div>
        </>
      ) : (
        <div className="row-actions">
          <button type="button" className="btn btn-primary btn-sm" onClick={handleConnect} disabled={connecting}>
            {connecting ? t("common.saving") : t("settings.slack.connectButton")}
          </button>
        </div>
      )}
    </div>
  );
}
