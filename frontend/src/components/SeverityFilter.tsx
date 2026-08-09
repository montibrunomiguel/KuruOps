import { useTranslation } from "react-i18next";
import type { Severity } from "../types/alerts";

// The severity <select> is identical between AlertsListPage and
// IncidentsListPage (same Severity enum, same option order) -- pulled out
// once both had drifted into copy-pasted duplicates, same reasoning as
// TagPicker/TimeRangeFilter already being shared.
export function SeverityFilter({
  value,
  onChange,
}: {
  value: Severity | "";
  onChange: (value: Severity | "") => void;
}) {
  const { t } = useTranslation();
  return (
    <select
      className="select"
      aria-label={t("dashboard.filters.severityFilterLabel")}
      value={value}
      onChange={(e) => onChange(e.target.value as Severity | "")}
    >
      <option value="">{t("dashboard.filters.allSeverities")}</option>
      <option value="critical">{t("common.severity.critical")}</option>
      <option value="high">{t("common.severity.high")}</option>
      <option value="medium">{t("common.severity.medium")}</option>
      <option value="low">{t("common.severity.low")}</option>
      <option value="informational">{t("common.severity.informational")}</option>
    </select>
  );
}
