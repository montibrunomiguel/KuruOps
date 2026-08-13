import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { useList, mutationErrorMessage } from "../../../api/hooks";
import type { Alert } from "../../../types/alerts";
import { shortId } from "../../../lib/format";

// Search-by-title/id/asset + click-to-link, same pattern AlertDetailPage's
// LinkedAlertsPanel already uses for alert-to-alert correlation -- a raw
// "type the alert's ID" text field (the previous version of this
// component) doesn't work in practice: every alert ID shown anywhere in
// the UI is truncated to 8 characters (see lib/format.shortId), so there
// was never a way to discover/copy a full UUID to paste in here, and
// submitting the truncated one always failed with "invalid alert id"
// (uuid.Parse rejecting a partial ID server-side). Also matches on
// asset/srcIp so an analyst can find related alerts by affected
// host/IP without already knowing the other alert's title.
export function LinkAlertForm({
  incidentId,
  linkedAlerts,
  onLinked,
}: {
  incidentId: string;
  linkedAlerts: Alert[];
  onLinked: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: candidates } = useList<Alert>((tk) => api.get<Alert[]>(`/api/v1/alerts?limit=50`, tk));
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);

  const results = (Array.isArray(candidates) ? candidates : []).filter((a) => {
    if (linkedAlerts.some((l) => l.id === a.id) || query.length === 0) return false;
    const q = query.toLowerCase();
    return (
      a.id.toLowerCase().includes(q) ||
      a.title.toLowerCase().includes(q) ||
      (a.asset ?? "").toLowerCase().includes(q) ||
      (a.srcIp ?? "").toLowerCase().includes(q)
    );
  });

  async function link(alertId: string) {
    setError(null);
    try {
      await api.put(`/api/v1/incidents/${incidentId}/alerts/${alertId}`, {}, token);
      setQuery("");
      onLinked();
    } catch (err) {
      setError(mutationErrorMessage(err));
    }
  }

  return (
    <div style={{ marginBottom: 12 }}>
      {error && <div className="error-banner">{error}</div>}
      <input
        className="input"
        style={{ width: "100%" }}
        placeholder={t("incidents.detail.linkAlertPlaceholder")}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />
      {results.length > 0 && (
        <div className="search-result-list">
          {results.map((a) => (
            <div className="search-result-item" key={a.id} onClick={() => link(a.id)}>
              <span className="mono">{shortId(a.id)}</span> · {a.title}
              {a.asset && <span className="helper-text"> · {a.asset}</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
