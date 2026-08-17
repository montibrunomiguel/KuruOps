import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";
import { AttachmentButton } from "./AttachmentButton";

// Shared comment/note composer for the alert and incident detail pages --
// same input+attachment+submit shape, previously duplicated as
// AddAlertCommentForm/AddCommentForm. kind picks the endpoint
// (/api/v1/<kind>s/<id>/comments) and the i18n namespace
// ("alerts.detail.*"/"incidents.detail.*") backing its copy.
export function AddNoteForm({ kind, id, onAdded }: { kind: "alert" | "incident"; id: string; onAdded: () => void }) {
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
      await api.post(`/api/v1/${kind}s/${id}/comments`, { body, attachmentUrl }, token);
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
        placeholder={t(`${kind}s.detail.addNotePlaceholder`)}
        value={body}
        onChange={(e) => setBody(e.target.value)}
      />
      <AttachmentButton kind={kind} id={id} value={attachmentUrl} onChange={setAttachmentUrl} disabled={submitting} />
      <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
        {submitting ? t(`${kind}s.detail.posting`) : t(`${kind}s.detail.post`)}
      </button>
    </form>
  );
}
