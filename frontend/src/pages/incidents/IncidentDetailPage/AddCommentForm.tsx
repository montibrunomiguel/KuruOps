import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import { AttachmentButton } from "../../../components/AttachmentButton";

export function AddCommentForm({ incidentId, onAdded }: { incidentId: string; onAdded: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [body, setBody] = useState("");
  const [attachmentUrl, setAttachmentUrl] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!body.trim()) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.post(`/api/v1/incidents/${incidentId}/comments`, { body, attachmentUrl }, token);
      setBody("");
      setAttachmentUrl(null);
      onAdded();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} style={{ marginTop: 14, display: "flex", gap: 8, alignItems: "flex-start" }}>
      {error && <div className="error-banner">{error}</div>}
      <input
        className="input"
        style={{ flex: 1 }}
        placeholder={t("incidents.detail.addNotePlaceholder")}
        value={body}
        onChange={(e) => setBody(e.target.value)}
      />
      <AttachmentButton kind="incident" id={incidentId} value={attachmentUrl} onChange={setAttachmentUrl} disabled={submitting} />
      <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
        {submitting ? t("incidents.detail.posting") : t("incidents.detail.post")}
      </button>
    </form>
  );
}
