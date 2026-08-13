import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { useList, mutationErrorMessage } from "../../../api/hooks";
import type { Alert } from "../../../types/alerts";
import { shortId } from "../../../lib/format";

export function LinkedAlertsPanel({ alertId }: { alertId: string }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: linked, reload } = useList<Alert>((tk) => api.get<Alert[]>(`/api/v1/alerts/${alertId}/alerts`, tk), [alertId]);
  const { data: candidates } = useList<Alert>((tk) => api.get<Alert[]>(`/api/v1/alerts?limit=50`, tk));
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);

  const results = (Array.isArray(candidates) ? candidates : []).filter(
    (a) =>
      a.id !== alertId &&
      !(linked ?? []).some((l) => l.id === a.id) &&
      query.length > 0 &&
      (a.id.toLowerCase().includes(query.toLowerCase()) || a.title.toLowerCase().includes(query.toLowerCase())),
  );

  async function link(otherId: string) {
    setError(null);
    try {
      await api.put(`/api/v1/alerts/${alertId}/alerts/${otherId}`, {}, token);
      setQuery("");
      reload();
    } catch (err) {
      setError(mutationErrorMessage(err));
    }
  }

  async function unlink(otherId: string) {
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
              <button type="button" onClick={() => unlink(a.id)} aria-label="unlink">
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      <input
        className="input"
        style={{ width: "100%" }}
        placeholder={t("alerts.detail.linkSearchPlaceholder")}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />
      {results.length > 0 && (
        <div className="search-result-list">
          {results.map((a) => (
            <div className="search-result-item" key={a.id} onClick={() => link(a.id)}>
              <span className="mono">{shortId(a.id)}</span> · {a.title}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
