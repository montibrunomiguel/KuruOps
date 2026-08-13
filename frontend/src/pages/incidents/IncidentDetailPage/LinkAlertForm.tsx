import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";

export function LinkAlertForm({ incidentId, onLinked }: { incidentId: string; onLinked: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [alertId, setAlertId] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!alertId.trim()) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.put(`/api/v1/incidents/${incidentId}/alerts/${alertId.trim()}`, {}, token);
      setAlertId("");
      onLinked();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="step-editor-row" style={{ marginBottom: 12 }}>
      {error && <div className="error-banner">{error}</div>}
      <input
        className="input"
        placeholder={t("incidents.detail.linkAlertPlaceholder")}
        value={alertId}
        onChange={(e) => setAlertId(e.target.value)}
      />
      <button type="submit" className="btn btn-sm" disabled={submitting}>
        {t("incidents.detail.link")}
      </button>
    </form>
  );
}
