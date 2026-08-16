import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { Tag } from "../../types/tags";
import { formatDateTime } from "../../lib/format";

// Tags are governed here, not typed freely on an alert/incident -- an
// analyst can only attach a tag that already exists in this catalog (see
// TagPicker), and cmd/ingest drops any webhook-supplied tag that isn't
// registered here instead of silently growing an uncontrolled tag set. This
// is also the same namespace domain.User.AllowedTags scopes access by, so
// this catalog is what makes "company" tags something an admin actually
// governs.
export function TagsPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: tags, loading, error, reload } = useList<Tag>((tk) => api.get<Tag[]>("/api/v1/tags", tk));
  const [showCreate, setShowCreate] = useState(false);
  // Inline confirm/cancel instead of window.confirm() -- some embedded
  // browser contexts silently auto-dismiss native confirm() dialogs, which
  // made delete look like it does nothing (see OnCallScheduleDetailPage).
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function remove(id: string) {
    setDeletingId(id);
    setDeleteError(null);
    try {
      await api.del(`/api/v1/settings/tags/${id}`, token);
      setConfirmingId(null);
      reload();
    } catch (err) {
      setDeleteError(mutationErrorMessage(err));
    } finally {
      setDeletingId(null);
    }
  }

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.tags.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("settings.tags.newTag")}
        </button>
      </div>
      <p className="helper-text" style={{ marginTop: -8, marginBottom: 14 }}>
        {t("settings.tags.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {deleteError && <div className="error-banner">{deleteError}</div>}

      {showCreate && (
        <CreateTagForm
          onCancel={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false);
            reload();
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && tags && tags.length === 0 && <div className="empty-state">{t("settings.tags.noTags")}</div>}
      {!loading &&
        tags &&
        tags.map((tag) => (
          <div className="row" key={tag.id}>
            <div className="row-main">
              <p className="row-title">
                <span
                  style={{
                    display: "inline-block",
                    width: 10,
                    height: 10,
                    borderRadius: "50%",
                    background: tag.color ?? "var(--accent)",
                    marginRight: 8,
                  }}
                />
                {tag.name}
              </p>
              <p className="row-sub">{t("settings.tags.createdOn", { date: formatDateTime(tag.createdAt) })}</p>
            </div>
            <div className="row-actions">
              {confirmingId === tag.id ? (
                <>
                  <span className="helper-text" style={{ flexBasis: "100%" }}>
                    {t("settings.tags.deleteConfirm")}
                  </span>
                  <button
                    className="btn btn-danger btn-sm"
                    disabled={deletingId === tag.id}
                    onClick={() => remove(tag.id)}
                  >
                    {deletingId === tag.id ? t("common.saving") : t("common.confirmDelete")}
                  </button>
                  <button className="btn btn-ghost btn-sm" onClick={() => setConfirmingId(null)}>
                    {t("common.cancel")}
                  </button>
                </>
              ) : (
                <button className="btn btn-danger btn-sm" onClick={() => setConfirmingId(tag.id)}>
                  {t("settings.tags.delete")}
                </button>
              )}
            </div>
          </div>
        ))}
    </div>
  );
}

function CreateTagForm({ onCancel, onCreated }: { onCancel: () => void; onCreated: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState("");
  const [color, setColor] = useState("#4f8cff");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await api.post("/api/v1/settings/tags", { name, color }, token);
      onCreated();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 14 }}>
      {error && <div className="error-banner">{error}</div>}
      <div className="form-grid">
        <div className="field">
          <label htmlFor="tag-name">{t("settings.tags.form.name")}</label>
          <input
            id="tag-name"
            className="input"
            placeholder={t("settings.tags.form.namePlaceholder")}
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="tag-color">{t("settings.tags.form.color")}</label>
          <input
            id="tag-color"
            type="color"
            className="input"
            style={{ padding: 3, width: 60 }}
            value={color}
            onChange={(e) => setColor(e.target.value)}
          />
        </div>
      </div>
      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.creating") : t("common.create")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}
