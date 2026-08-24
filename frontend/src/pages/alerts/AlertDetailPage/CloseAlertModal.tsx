import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { Alert, Classification } from "../../../types/alerts";
import { AttachmentButton } from "../../../components/AttachmentButton";
import { Modal } from "../../../components/Modal";

// The "Fechar e Classificar" form, as a modal (same modal-overlay/modal
// pattern as IncidentsListPage's CreateIncidentForm) -- triggered by the
// header button next to Analisar com IA instead of an always-mounted panel
// in the main content column.
export function CloseAlertModal({ alert, onClose, onSaved }: { alert: Alert; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [classification, setClassification] = useState<Classification>("true_positive");
  const [comment, setComment] = useState("");
  const [attachmentUrl, setAttachmentUrl] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await api.post(`/api/v1/alerts/${alert.id}/close`, { classification, comment, attachmentUrl }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal onClose={onClose} as="form" onSubmit={handleSubmit}>
      <div className="panel-header">
        <h2 className="modal-title" style={{ marginBottom: 0 }}>
          {t("alerts.detail.closeAndClassify")}
        </h2>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label={t("common.close")}>
          ×
        </button>
      </div>
      {error && <div className="error-banner">{error}</div>}
      <div style={{ display: "flex", flexDirection: "column", gap: 8, marginBottom: 12 }}>
        {(["false_positive", "true_positive", "authorized_event"] as Classification[]).map((c) => (
          <label
            key={c}
            style={{
              display: "flex",
              alignItems: "flex-start",
              gap: 10,
              padding: "8px 10px",
              borderRadius: 7,
              border: "1px solid var(--border-strong)",
              cursor: "pointer",
            }}
          >
            <input
              type="radio"
              name="classification"
              checked={classification === c}
              onChange={() => setClassification(c)}
              style={{ marginTop: 3 }}
            />
            <span>
              <span style={{ display: "block", fontWeight: 600, fontSize: 12.5 }}>{t(`common.classification.${c}`)}</span>
              <span style={{ display: "block", fontSize: 11, color: "var(--text-muted)" }}>
                {t(`common.classificationHint.${c}`)}
              </span>
            </span>
          </label>
        ))}
      </div>
      <textarea
        className="textarea"
        style={{ width: "100%", marginBottom: 10 }}
        placeholder={t("alerts.detail.closingComment")}
        value={comment}
        onChange={(e) => setComment(e.target.value)}
      />
      <div className="row-actions" style={{ marginBottom: 10 }}>
        <AttachmentButton kind="alert" id={alert.id} value={attachmentUrl} onChange={setAttachmentUrl} disabled={submitting} />
      </div>
      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting} style={{ flex: 1, justifyContent: "center" }}>
          {submitting ? t("common.saving") : t("alerts.detail.confirmClose")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose}>
          {t("common.cancel")}
        </button>
      </div>
    </Modal>
  );
}
