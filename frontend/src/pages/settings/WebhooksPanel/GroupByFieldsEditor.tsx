import { useTranslation } from "react-i18next";

// GroupByFieldsEditor: a dynamic list of JSON-path inputs (e.g. "host.name")
// plus the dedup window in minutes -- used both in CreateWebhookModal and in
// EditWebhookModal, same "controlled list, lift state to the parent" shape
// as FieldMappingTemplatesPanel's rule list.
export function GroupByFieldsEditor({
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
