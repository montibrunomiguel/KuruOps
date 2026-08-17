import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { FieldMappingTemplate, WebhookEndpoint } from "../../../types/api";
import { expiryOptions, expiryToDays } from "../../../lib/format";
import { GroupByFieldsEditor } from "./GroupByFieldsEditor";

const DEFAULT_DEDUP_WINDOW_MINUTES = "30";

export function CreateWebhookModal({
  templates,
  onCancel,
  onCreated,
}: {
  templates: FieldMappingTemplate[];
  onCancel: () => void;
  onCreated: (name: string, token: string) => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState("");
  const [source, setSource] = useState("");
  const [expiresInDays, setExpiresInDays] = useState("90");
  const [fieldMappingTemplateId, setFieldMappingTemplateId] = useState("");
  const [groupByFields, setGroupByFields] = useState<string[]>([]);
  const [dedupWindowMinutes, setDedupWindowMinutes] = useState(DEFAULT_DEDUP_WINDOW_MINUTES);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    // Blank rows are dropped client-side too, same principle as
    // FieldMappingTemplatesPanel's TemplateForm.
    const cleanFields = groupByFields.map((f) => f.trim()).filter(Boolean);
    try {
      const res = await api.post<{ endpoint: WebhookEndpoint; token: string }>(
        "/api/v1/settings/webhooks",
        {
          name,
          source,
          expiresInDays: expiryToDays(expiresInDays),
          fieldMappingTemplateId: fieldMappingTemplateId || undefined,
          groupByFields: cleanFields.length ? cleanFields : undefined,
          dedupWindowMinutes: cleanFields.length ? Number(dedupWindowMinutes) : undefined,
        },
        token,
      );
      onCreated(name, res.token);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  const options = expiryOptions(t);

  return (
    <div className="modal-overlay" onClick={onCancel}>
      <form onSubmit={handleSubmit} className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="panel-header">
          <h2 className="modal-title" style={{ marginBottom: 0 }}>
            {t("settings.webhooks.newEndpointTitle")}
          </h2>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel} aria-label={t("common.close")}>
            ×
          </button>
        </div>
        {error && <div className="error-banner">{error}</div>}
        <div className="form-grid">
          <div className="field">
            <label htmlFor="wh-name">{t("settings.webhooks.form.name")}</label>
            <input id="wh-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
          </div>
          <div className="field">
            <label htmlFor="wh-source">{t("settings.webhooks.form.source")}</label>
            <input
              id="wh-source"
              className="input"
              placeholder={t("settings.webhooks.form.sourcePlaceholder")}
              value={source}
              onChange={(e) => setSource(e.target.value)}
              required
            />
          </div>
          <div className="field">
            <label htmlFor="wh-expiry">{t("settings.webhooks.form.tokenExpiry")}</label>
            <select id="wh-expiry" className="select" value={expiresInDays} onChange={(e) => setExpiresInDays(e.target.value)}>
              {options.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="wh-field-mapping-template">{t("settings.webhooks.fieldMappingTemplate")}</label>
            <select
              id="wh-field-mapping-template"
              className="select"
              value={fieldMappingTemplateId}
              onChange={(e) => setFieldMappingTemplateId(e.target.value)}
            >
              <option value="">{t("settings.webhooks.noFieldMappingTemplate")}</option>
              {templates.map((tmpl) => (
                <option key={tmpl.id} value={tmpl.id}>
                  {tmpl.name}
                </option>
              ))}
            </select>
          </div>
        </div>

        <GroupByFieldsEditor
          fields={groupByFields}
          windowMinutes={dedupWindowMinutes}
          onFieldsChange={setGroupByFields}
          onWindowChange={setDedupWindowMinutes}
        />

        <div className="row-actions" style={{ marginTop: 10 }}>
          <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
            {submitting ? t("common.creating") : t("common.create")}
          </button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
            {t("common.cancel")}
          </button>
        </div>
      </form>
    </div>
  );
}
