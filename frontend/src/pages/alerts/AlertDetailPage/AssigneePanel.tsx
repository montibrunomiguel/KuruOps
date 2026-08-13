import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { useList, mutationErrorMessage } from "../../../api/hooks";
import type { Alert } from "../../../types/alerts";
import type { UserSummary } from "../../../types/users";

export function AssigneePanel({ alert, onSaved }: { alert: Alert; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: directory } = useList<UserSummary>((tk) => api.get<UserSummary[]>("/api/v1/users/directory", tk));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save(analystId: string) {
    setSubmitting(true);
    setError(null);
    try {
      await api.put(`/api/v1/alerts/${alert.id}/assignee`, { analystId: analystId || null }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.assigneeTitle")}
      </h2>
      {error && <div className="error-banner">{error}</div>}
      <select
        className="select"
        style={{ width: "100%" }}
        value={alert.assignedAnalystId ?? ""}
        disabled={submitting}
        onChange={(e) => save(e.target.value)}
        aria-label={t("alerts.detail.assigneeTitle")}
      >
        <option value="">{t("common.unassigned")}</option>
        {(directory ?? []).map((u) => (
          <option key={u.id} value={u.id}>
            {u.name}
          </option>
        ))}
      </select>
    </div>
  );
}
