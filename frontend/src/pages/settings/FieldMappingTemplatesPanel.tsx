import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { mutationErrorMessage } from "../../api/hooks";
import { useAdminCrud } from "../../hooks/useAdminCrud";
import type { FieldMappingRule, FieldMappingTemplate } from "../../types/api";

// A Settings-managed catalog of JSON-path -> display-label rules (see
// domain.FieldMappingTemplate on the backend), reusable across several
// webhook endpoints (WebhooksPanel assigns one per endpoint, changeable
// after creation). Applied on ingest in addition to, not instead of, the
// sender's own top-level "metadata" object -- a rule is skipped if its
// label already came through automatically (see
// internal/ingest/field_mapping.go's applyFieldMappingTemplate).
export function FieldMappingTemplatesPanel() {
  const { t } = useTranslation();
  const {
    data: templates,
    loading,
    error,
    reload,
    showCreate,
    setShowCreate,
    confirming: confirmingId,
    confirm,
    cancel,
    deletingId,
    deleteError,
    remove,
  } = useAdminCrud<FieldMappingTemplate>("/api/v1/settings/field-mapping-templates");
  const [editingId, setEditingId] = useState<string | null>(null);

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.fieldMappingTemplates.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("settings.fieldMappingTemplates.newTemplate")}
        </button>
      </div>
      <p className="helper-text" style={{ marginTop: -8, marginBottom: 14 }}>
        {t("settings.fieldMappingTemplates.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {deleteError && <div className="error-banner">{deleteError}</div>}

      {showCreate && (
        <TemplateForm
          onCancel={() => setShowCreate(false)}
          onSaved={() => {
            setShowCreate(false);
            reload();
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && templates && templates.length === 0 && (
        <div className="empty-state">{t("settings.fieldMappingTemplates.noTemplates")}</div>
      )}

      {!loading &&
        templates &&
        templates.map((tmpl) =>
          editingId === tmpl.id ? (
            <TemplateForm
              key={tmpl.id}
              existing={tmpl}
              onCancel={() => setEditingId(null)}
              onSaved={() => {
                setEditingId(null);
                reload();
              }}
            />
          ) : (
            <div className="row" key={tmpl.id}>
              <div className="row-main">
                <p className="row-title">{tmpl.name}</p>
                <p className="row-sub">
                  {t("settings.fieldMappingTemplates.rulesCount", { count: tmpl.rules.length })}
                  {tmpl.rules.length > 0 && ` · ${tmpl.rules.map((r) => r.label).join(", ")}`}
                </p>
              </div>
              <div className="row-actions">
                {confirmingId === tmpl.id ? (
                  <>
                    <span className="helper-text" style={{ flexBasis: "100%" }}>
                      {t("settings.fieldMappingTemplates.deleteConfirm")}
                    </span>
                    <button
                      className="btn btn-danger btn-sm"
                      disabled={deletingId === tmpl.id}
                      onClick={() => remove(tmpl.id)}
                    >
                      {deletingId === tmpl.id ? t("common.saving") : t("common.confirmDelete")}
                    </button>
                    <button className="btn btn-ghost btn-sm" onClick={cancel}>
                      {t("common.cancel")}
                    </button>
                  </>
                ) : (
                  <>
                    <button className="btn btn-sm" onClick={() => setEditingId(tmpl.id)}>
                      {t("settings.fieldMappingTemplates.edit")}
                    </button>
                    <button className="btn btn-danger btn-sm" onClick={() => confirm(tmpl.id)}>
                      {t("settings.fieldMappingTemplates.delete")}
                    </button>
                  </>
                )}
              </div>
            </div>
          ),
        )}
    </div>
  );
}

// See TemplateForm's rules useState call below for why key exists --
// stripped again before this ever reaches the save payload.
interface EditableRule extends FieldMappingRule {
  key: string;
}

function TemplateForm({
  existing,
  onCancel,
  onSaved,
}: {
  existing?: FieldMappingTemplate;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState(existing?.name ?? "");
  // EditableRule adds a client-only React list key (crypto.randomUUID(),
  // stripped again in handleSubmit's cleanRules below) -- FieldMappingRule
  // itself has no id, and this list is reorderable via removeRule, so a
  // positional key would misattribute a row's focus/DOM state after a
  // removal shifts every later row's index.
  const [rules, setRules] = useState<EditableRule[]>(
    existing?.rules.length ? existing.rules.map((r) => ({ ...r, key: crypto.randomUUID() })) : [{ jsonPath: "", label: "", key: crypto.randomUUID() }],
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function updateRule(idx: number, field: keyof FieldMappingRule, value: string) {
    setRules((prev) => prev.map((r, i) => (i === idx ? { ...r, [field]: value } : r)));
  }

  function removeRule(idx: number) {
    setRules((prev) => prev.filter((_, i) => i !== idx));
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    // Blank rows are dropped client-side too (not just server-side by
    // FieldMappingTemplateService.validateFieldMappingTemplate) so an empty
    // trailing row added via "+ Add rule" and never filled in doesn't
    // round-trip back as a visible-but-useless row after save.
    const cleanRules: FieldMappingRule[] = rules
      .filter((r) => r.jsonPath.trim() && r.label.trim())
      .map((r) => ({ jsonPath: r.jsonPath, label: r.label }));
    try {
      if (existing) {
        await api.put(`/api/v1/settings/field-mapping-templates/${existing.id}`, { name, rules: cleanRules }, token);
      } else {
        await api.post("/api/v1/settings/field-mapping-templates", { name, rules: cleanRules }, token);
      }
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 14 }}>
      {error && <div className="error-banner">{error}</div>}
      <div className="field">
        <label htmlFor="fmt-name">{t("settings.fieldMappingTemplates.form.name")}</label>
        <input
          id="fmt-name"
          className="input"
          placeholder={t("settings.fieldMappingTemplates.form.namePlaceholder")}
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
      </div>

      <div className="field" style={{ marginTop: 10 }}>
        <label>{t("settings.fieldMappingTemplates.form.rules")}</label>
        {rules.map((rule, idx) => (
          <div key={rule.key} className="form-grid" style={{ marginBottom: 6, alignItems: "end" }}>
            <div className="field">
              <input
                className="input"
                aria-label={t("settings.fieldMappingTemplates.form.jsonPath")}
                placeholder={t("settings.fieldMappingTemplates.form.jsonPathPlaceholder")}
                value={rule.jsonPath}
                onChange={(e) => updateRule(idx, "jsonPath", e.target.value)}
              />
            </div>
            <div className="field">
              <input
                className="input"
                aria-label={t("settings.fieldMappingTemplates.form.label")}
                placeholder={t("settings.fieldMappingTemplates.form.labelPlaceholder")}
                value={rule.label}
                onChange={(e) => updateRule(idx, "label", e.target.value)}
              />
            </div>
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => removeRule(idx)}
              aria-label={t("settings.fieldMappingTemplates.form.removeRule")}
            >
              ×
            </button>
          </div>
        ))}
        <button type="button" className="btn btn-sm" onClick={() => setRules((prev) => [...prev, { jsonPath: "", label: "", key: crypto.randomUUID() }])}>
          {t("settings.fieldMappingTemplates.form.addRule")}
        </button>
        <p className="helper-text" style={{ marginTop: 6 }}>
          {t("settings.fieldMappingTemplates.conflictHint")}
        </p>
      </div>

      <div className="row-actions" style={{ marginTop: 10 }}>
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}
