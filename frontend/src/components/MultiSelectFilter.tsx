import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

export interface MultiSelectOption {
  value: string;
  label: string;
}

// A multi-select for a fixed, static list of options (severity, status) --
// same "chips + a <select> to add another" idiom TagPicker/AssigneePicker
// already use for their own (API-sourced) catalogs, so every multi-select
// control in the app looks and behaves the same way. Kept separate from
// SeverityFilter, which wraps a single-value <select> and is
// still used as-is on AlertsListPage/IncidentsListPage -- changing its
// value shape would break those single-select call sites.
export function MultiSelectFilter({
  options,
  value,
  onChange,
  placeholder,
  ariaLabel,
}: {
  options: MultiSelectOption[];
  value: string[];
  onChange: (next: string[]) => void;
  placeholder: string;
  ariaLabel: string;
}) {
  const { t } = useTranslation();
  const byValue = new Map(options.map((o) => [o.value, o.label]));
  const available = options.filter((o) => !value.includes(o.value));

  function remove(v: string) {
    onChange(value.filter((x) => x !== v));
  }

  const chips: ReactNode = value.length > 0 && (
    <div className="tag-chip-list" style={{ display: "inline-flex", flexWrap: "wrap", gap: 4, marginRight: 6 }}>
      {value.map((v) => (
        <span className="tag-chip" key={v}>
          {byValue.get(v) ?? v}
          <button type="button" onClick={() => remove(v)} aria-label={t("common.removeFilterValue", { value: byValue.get(v) ?? v })}>
            ×
          </button>
        </span>
      ))}
    </div>
  );

  return (
    <div style={{ display: "inline-flex", alignItems: "center" }}>
      {chips}
      <select
        className="select"
        aria-label={ariaLabel}
        value=""
        onChange={(e) => {
          if (e.target.value) onChange([...value, e.target.value]);
        }}
      >
        <option value="">{value.length === 0 ? placeholder : t("common.addFilterOption")}</option>
        {available.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </div>
  );
}
