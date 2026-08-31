import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { useList, mutationErrorMessage } from "../../../api/hooks";
import type { Alert } from "../../../types/alerts";
import { shortId } from "../../../lib/format";
import { LinkSearchPicker } from "../../../components/LinkSearchPicker";
import { useConfirm } from "../../../hooks/useConfirm";

export function LinkedAlertsPanel({ alertId }: { alertId: string }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: linked, reload } = useList<Alert>(["alert-linked-alerts", alertId], (tk) => api.get<Alert[]>(`/api/v1/alerts/${alertId}/alerts`, tk));
  const { data: candidates } = useList<Alert>(["alert-link-candidates"], (tk) => api.get<Alert[]>(`/api/v1/alerts?limit=50`, tk));
  const [error, setError] = useState<string | null>(null);
  const { confirming, confirm, cancel } = useConfirm<string>();

  const excludeIds = new Set([alertId, ...(linked ?? []).map((l) => l.id)]);

  async function link(otherId: string) {
    setError(null);
    try {
      await api.put(`/api/v1/alerts/${alertId}/alerts/${otherId}`, {}, token);
      reload();
    } catch (err) {
      setError(mutationErrorMessage(err));
    }
  }

  async function unlink(otherId: string) {
    cancel();
    setError(null);
    try {
      await api.del(`/api/v1/alerts/${alertId}/alerts/${otherId}`, token);
      reload();
    } catch (err) {
      setError(mutationErrorMessage(err));
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.linkedAlertsTitle")}
      </h2>
      {error && <div className="error-banner">{error}</div>}
      {linked && linked.length === 0 && <div className="empty-state">{t("alerts.detail.noLinkedAlerts")}</div>}
      {linked && linked.length > 0 && (
        <div style={{ marginBottom: 10 }}>
          {linked.map((a) => (
            <span className="linked-chip" key={a.id}>
              <span className="mono">{shortId(a.id)}</span> · {a.title}
              {confirming === a.id ? (
                <>
                  <button
                    type="button"
                    onClick={() => unlink(a.id)}
                    aria-label={t("common.confirm")}
                    title={t("alerts.detail.unlinkConfirm", { id: shortId(a.id) }) ?? undefined}
                  >
                    ✓
                  </button>
                  <button type="button" onClick={cancel} aria-label={t("common.cancel")}>
                    ✗
                  </button>
                </>
              ) : (
                <button type="button" onClick={() => confirm(a.id)} aria-label={t("alerts.detail.unlinkAlert", { id: shortId(a.id) })}>
                  ×
                </button>
              )}
            </span>
          ))}
        </div>
      )}
      <LinkSearchPicker
        candidates={candidates ?? []}
        excludeIds={excludeIds}
        onLink={link}
        placeholder={t("alerts.detail.linkSearchPlaceholder")}
      />
    </div>
  );
}
