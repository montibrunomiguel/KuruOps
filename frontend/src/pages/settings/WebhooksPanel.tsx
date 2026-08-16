import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { FieldMappingTemplate, WebhookEndpoint } from "../../types/api";
import { formatDateTime } from "../../lib/format";

function expiryOptions(t: (k: string) => string): { value: string; label: string }[] {
  return [
    { value: "30", label: t("settings.webhooks.expiry.30") },
    { value: "90", label: t("settings.webhooks.expiry.90") },
    { value: "180", label: t("settings.webhooks.expiry.180") },
    { value: "365", label: t("settings.webhooks.expiry.365") },
    { value: "never", label: t("settings.webhooks.expiry.never") },
  ];
}

function expiryToDays(value: string): number | undefined {
  // undefined -> let the backend apply its own default (90d); "never" -> 0,
  // which service.resolveExpiry treats as an explicit opt-out.
  if (value === "never") return 0;
  return Number(value);
}

const DEFAULT_DEDUP_WINDOW_MINUTES = "30";

// GroupByFieldsEditor: a dynamic list of JSON-path inputs (e.g. "host.name")
// plus the dedup window in minutes -- used both in CreateWebhookModal and in
// EditWebhookModal, same "controlled list, lift state to the parent" shape
// as FieldMappingTemplatesPanel's rule list.
function GroupByFieldsEditor({
  fields,
  windowMinutes,
  onFieldsChange,
  onWindowChange,
}: {
  fields: string[];
  windowMinutes: string;
  onFieldsChange: (fields: string[]) => void;
  onWindowChange: (value: string) => void;
}) {
  const { t } = useTranslation();

  function updateField(idx: number, value: string) {
    onFieldsChange(fields.map((f, i) => (i === idx ? value : f)));
  }

  function removeField(idx: number) {
    onFieldsChange(fields.filter((_, i) => i !== idx));
  }

  return (
    <div className="field" style={{ marginTop: 10 }}>
      <label>{t("settings.webhooks.groupByFields.label")}</label>
      <p className="helper-text" style={{ marginTop: -2, marginBottom: 6 }}>
        {t("settings.webhooks.groupByFields.help")}
      </p>
      {fields.map((f, idx) => (
        <div key={idx} className="form-grid" style={{ marginBottom: 6, alignItems: "end" }}>
          <div className="field">
            <input
              className="input"
              aria-label={t("settings.webhooks.groupByFields.fieldLabel")}
              placeholder={t("settings.webhooks.groupByFields.placeholder")}
              value={f}
              onChange={(e) => updateField(idx, e.target.value)}
            />
          </div>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => removeField(idx)}
            aria-label={t("settings.webhooks.groupByFields.removeField")}
          >
            ×
          </button>
        </div>
      ))}
      <button type="button" className="btn btn-sm" onClick={() => onFieldsChange([...fields, ""])}>
        {t("settings.webhooks.groupByFields.addField")}
      </button>
      {fields.length > 0 && (
        <div className="field" style={{ marginTop: 8, maxWidth: 180 }}>
          <label htmlFor="wh-dedup-window">{t("settings.webhooks.groupByFields.window")}</label>
          <input
            id="wh-dedup-window"
            type="number"
            min={1}
            className="input"
            value={windowMinutes}
            onChange={(e) => onWindowChange(e.target.value)}
            // select-on-focus: this field starts pre-filled with
            // DEFAULT_DEDUP_WINDOW_MINUTES, so clicking in and typing a new
            // value without clearing first appends instead of replacing
            // (e.g. "30" + "10" -> "3010").
            onFocus={(e) => e.target.select()}
          />
        </div>
      )}
    </div>
  );
}

// EXPIRING_SOON_DAYS controls when a live token starts showing the
// "expiring soon" warning instead of the plain expiry date -- gives an
// admin a heads-up window to regenerate before ingest starts rejecting it.
const EXPIRING_SOON_DAYS = 14;

function expiryBadge(t: (k: string, opts?: Record<string, unknown>) => string, expiresAt?: string) {
  if (!expiresAt) return <span className="badge badge-muted">{t("settings.webhooks.badge.neverExpires")}</span>;
  const diffDays = (new Date(expiresAt).getTime() - Date.now()) / 86_400_000;
  if (diffDays < 0) {
    return (
      <span className="badge badge-critical">
        {t("settings.webhooks.badge.expiredOn", { date: formatDateTime(expiresAt) })}
      </span>
    );
  }
  if (diffDays <= EXPIRING_SOON_DAYS) {
    return (
      <span className="badge badge-sev-high">
        {t("settings.webhooks.badge.expiringSoon", { date: formatDateTime(expiresAt) })}
      </span>
    );
  }
  return (
    <span className="badge badge-muted">
      {t("settings.webhooks.badge.expiresOn", { date: formatDateTime(expiresAt) })}
    </span>
  );
}

export function WebhooksPanel() {
  const { t } = useTranslation();
  const { data: endpoints, loading, error, reload } = useList<WebhookEndpoint>(
    (tk) => api.get<WebhookEndpoint[]>("/api/v1/settings/webhooks", tk),
  );
  // Fetched once here and passed down to both modals, instead of each of
  // them fetching its own copy.
  const { data: templates } = useList<FieldMappingTemplate>((tk) =>
    api.get<FieldMappingTemplate[]>("/api/v1/settings/field-mapping-templates", tk),
  );

  const [showCreate, setShowCreate] = useState(false);
  // Looked up by id against the live `endpoints` list (not a held snapshot)
  // so a save inside EditWebhookModal -- which reloads the list -- is
  // reflected immediately in the still-open modal, same "editingId" pattern
  // FieldMappingTemplatesPanel already uses for its own edit-in-place flow.
  const [editingEndpointId, setEditingEndpointId] = useState<string | null>(null);
  const editingEndpoint = endpoints?.find((ep) => ep.id === editingEndpointId) ?? null;
  const [newToken, setNewToken] = useState<{ name: string; token: string } | null>(null);

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.webhooks.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("settings.webhooks.newEndpoint")}
        </button>
      </div>

      {error && <div className="error-banner">{error}</div>}

      {newToken && (
        <div className="panel" style={{ background: "var(--accent-soft)", borderColor: "var(--accent)", marginBottom: 14 }}>
          <p style={{ margin: "0 0 8px", fontSize: 12.5 }}>
            {t("settings.webhooks.tokenGenerated", { name: newToken.name })}
          </p>
          <div className="token-reveal">
            <code style={{ fontSize: 12 }}>{newToken.token}</code>
            <button
              className="btn btn-sm"
              onClick={() => {
                navigator.clipboard.writeText(newToken.token);
              }}
            >
              {t("common.copy")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setNewToken(null)}>
              {t("common.close")}
            </button>
          </div>
        </div>
      )}

      {showCreate && (
        <CreateWebhookModal
          templates={templates ?? []}
          onCancel={() => setShowCreate(false)}
          onCreated={(name, tok) => {
            setShowCreate(false);
            setNewToken({ name, token: tok });
            reload();
          }}
        />
      )}

      {editingEndpoint && (
        <EditWebhookModal
          endpoint={editingEndpoint}
          templates={templates ?? []}
          onClose={() => setEditingEndpointId(null)}
          onChanged={reload}
          onRegenerated={(tok) => {
            setNewToken({ name: editingEndpoint.name, token: tok });
            setEditingEndpointId(null);
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && endpoints && endpoints.length === 0 && (
        <div className="empty-state">{t("settings.webhooks.noEndpoints")}</div>
      )}

      {!loading &&
        endpoints &&
        endpoints.map((ep) => (
          <div
            className="row"
            key={ep.id}
            style={{ cursor: "pointer" }}
            onClick={() => setEditingEndpointId(ep.id)}
          >
            <div className="row-main">
              <p className="row-title">
                {ep.name}{" "}
                <span className={`badge ${ep.status === "active" ? "badge-success" : "badge-muted"}`}>
                  <span className="badge-status-dot" />
                  {ep.status}
                </span>{" "}
                {expiryBadge(t, ep.expiresAt)}
              </p>
              <p className="row-sub">
                {ep.source} · token whk_••••••••{ep.tokenLast4}
              </p>
              <p className="row-sub">
                {ep.groupByFields.length
                  ? t("settings.webhooks.groupByFields.summary", {
                      fields: ep.groupByFields.join(", "),
                      minutes: ep.dedupWindowMinutes,
                    })
                  : t("settings.webhooks.groupByFields.summaryOff")}
              </p>
            </div>
          </div>
        ))}
    </div>
  );
}

function CreateWebhookModal({
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

function EditWebhookModal({
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
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
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
      </div>
    </div>
  );
}
