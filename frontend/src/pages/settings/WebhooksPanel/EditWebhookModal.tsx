import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { FieldMappingTemplate, WebhookEndpoint } from "../../../types/api";
import { expiryOptions, expiryToDays } from "../../../lib/format";
import { GroupByFieldsEditor } from "./GroupByFieldsEditor";
import { expiryBadge } from "./expiryBadge";
import { Modal } from "../../../components/Modal";

export function EditWebhookModal({
  endpoint,
  templates,
  onClose,
  onChanged,
  onRegenerated,
}: {
  endpoint: WebhookEndpoint;
  templates: FieldMappingTemplate[];
  onClose: () => void;
  onChanged: () => void;
  onRegenerated: (token: string) => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [busy, setBusy] = useState(false);
  const [regenExpiry, setRegenExpiry] = useState("90");
  const [fieldMappingTemplateId, setFieldMappingTemplateId] = useState(endpoint.fieldMappingTemplateId ?? "");
  const [error, setError] = useState<string | null>(null);
  const [groupByFields, setGroupByFields] = useState<string[]>(
    endpoint.groupByFields.length ? endpoint.groupByFields : [""],
  );
  const [dedupWindowMinutes, setDedupWindowMinutes] = useState(String(endpoint.dedupWindowMinutes));

  async function saveFieldMappingTemplate() {
    setBusy(true);
    setError(null);
    try {
      await api.put(
        `/api/v1/settings/webhooks/${endpoint.id}/field-mapping-template`,
        { templateId: fieldMappingTemplateId || null },
        token,
      );
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function saveGroupByFields() {
    setBusy(true);
    setError(null);
    const cleanFields = groupByFields.map((f) => f.trim()).filter(Boolean);
    try {
      await api.put(
        `/api/v1/settings/webhooks/${endpoint.id}/group-by-fields`,
        { groupByFields: cleanFields, dedupWindowMinutes: Number(dedupWindowMinutes) },
        token,
      );
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function toggleStatus() {
    setBusy(true);
    setError(null);
    try {
      const action = endpoint.status === "active" ? "disable" : "enable";
      await api.post(`/api/v1/settings/webhooks/${endpoint.id}/${action}`, {}, token);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function regenerate() {
    setBusy(true);
    setError(null);
    try {
      const res = await api.post<{ token: string }>(
        `/api/v1/settings/webhooks/${endpoint.id}/regenerate`,
        { expiresInDays: expiryToDays(regenExpiry) },
        token,
      );
      onRegenerated(res.token);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
      setBusy(false);
    }
  }

  const options = expiryOptions(t);

  return (
    <Modal onClose={onClose} label={endpoint.name}>
      <div className="panel-header">
        <div>
          <h2 className="modal-title" style={{ marginBottom: 6 }}>
            {endpoint.name}
          </h2>
          <p className="row-sub" style={{ margin: 0 }}>
            <span className={`badge ${endpoint.status === "active" ? "badge-success" : "badge-muted"}`}>
              <span className="badge-status-dot" />
              {endpoint.status}
            </span>{" "}
            {expiryBadge(t, endpoint.expiresAt)}
          </p>
        </div>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label={t("common.close")}>
          ×
        </button>
      </div>

      <p className="row-sub">
        {endpoint.source} · token whk_••••••••{endpoint.tokenLast4}
      </p>

      {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}

      <div className="field" style={{ marginTop: 14 }}>
        <label htmlFor="wh-edit-field-mapping-template">{t("settings.webhooks.fieldMappingTemplate")}</label>
        <div style={{ display: "flex", gap: 6 }}>
          <select
            id="wh-edit-field-mapping-template"
            className="select"
            style={{ flex: 1 }}
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
          <button
            className="btn btn-sm"
            onClick={saveFieldMappingTemplate}
            disabled={busy || fieldMappingTemplateId === (endpoint.fieldMappingTemplateId ?? "")}
          >
            {t("settings.webhooks.changeTemplate")}
          </button>
        </div>
      </div>

      <GroupByFieldsEditor
        fields={groupByFields}
        windowMinutes={dedupWindowMinutes}
        onFieldsChange={setGroupByFields}
        onWindowChange={setDedupWindowMinutes}
      />
      <div className="row-actions" style={{ marginTop: 6 }}>
        <button className="btn btn-sm" onClick={saveGroupByFields} disabled={busy}>
          {t("common.save")}
        </button>
      </div>

      <div className="field" style={{ marginTop: 14 }}>
        <label htmlFor="wh-edit-regen-expiry">{t("settings.webhooks.regenExpiryTitle")}</label>
        <div style={{ display: "flex", gap: 6 }}>
          <select
            id="wh-edit-regen-expiry"
            className="select"
            style={{ flex: 1 }}
            value={regenExpiry}
            onChange={(e) => setRegenExpiry(e.target.value)}
          >
            {options.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <button className="btn btn-sm" onClick={regenerate} disabled={busy}>
            {t("settings.webhooks.regenerate")}
          </button>
        </div>
      </div>

      <div className="row-actions" style={{ marginTop: 16, justifyContent: "space-between" }}>
        <button className="btn btn-sm" onClick={toggleStatus} disabled={busy}>
          {endpoint.status === "active" ? t("common.disable") : t("common.enable")}
        </button>
        <button className="btn btn-ghost btn-sm" onClick={onClose}>
          {t("common.close")}
        </button>
      </div>
    </Modal>
  );
}
