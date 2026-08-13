import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { Incident } from "../../../types/incidents";

export function DescriptionPanel({ incident, onSaved }: { incident: Incident; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(incident.description);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    setSubmitting(true);
    setError(null);
    try {
      await api.put(`/api/v1/incidents/${incident.id}/description`, { description: value }, token);
      setEditing(false);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("incidents.detail.descriptionTitle")}</h2>
        {!editing && (
          <button
            className="btn btn-ghost btn-sm"
            style={{ color: "var(--accent)" }}
            onClick={() => {
              setValue(incident.description);
              setEditing(true);
            }}
          >
            {t("common.edit")}
          </button>
        )}
      </div>
      {error && <div className="error-banner">{error}</div>}
      {editing ? (
        <>
          <textarea className="textarea" style={{ width: "100%", minHeight: 100 }} value={value} onChange={(e) => setValue(e.target.value)} />
          <div className="row-actions" style={{ marginTop: 10 }}>
            <button className="btn btn-primary btn-sm" disabled={submitting} onClick={save}>
              {submitting ? t("common.saving") : t("common.save")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setEditing(false)}>
              {t("common.cancel")}
            </button>
          </div>
        </>
      ) : (
        <p style={{ margin: 0, fontSize: 13, whiteSpace: "pre-wrap" }}>
          {incident.description || <span style={{ color: "var(--text-muted)" }}>{t("incidents.detail.descriptionEmpty")}</span>}
        </p>
      )}
    </div>
  );
}
