import { useTranslation } from "react-i18next";

export interface MultiSelectOption {
  value: string;
  label: string;
}

// A multi-select for a fixed, static list of options (severity, status) --
// the exact same "chips + a <select> to add another" markup TagPicker uses
// for its own (API-sourced) catalog: chips and the trailing <select> as
// direct children of one .tag-picker box, not two loose elements sitting
// side by side in the filter bar's own flex row. That used to be the
// difference between this control reading as one thing (a bordered pill)
// and reading as three (a chip, a chip, a dropdown) -- see AssigneePicker
// for the other control this same fix applies to. Kept separate from
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

  return (
    <div className="tag-picker">
      {value.map((v) => (
        <span className="tag-chip" key={v}>
          {byValue.get(v) ?? v}
          <button type="button" onClick={() => remove(v)} aria-label={t("common.removeFilterValue", { value: byValue.get(v) ?? v })}>
            ×
          </button>
        </span>
      ))}
      <select
        className="tag-picker-add"
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
