import { Fragment, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { Severity } from "../types/alerts";
import type { IncidentPriority } from "../types/incidents";
import { PRIORITY_ORDER } from "../lib/chartColors";

export const SEVERITY_PRIORITY_GRID_SEVERITIES: Severity[] = [
  "critical",
  "high",
  "medium",
  "low",
  "informational",
];

// Shared 5x4 severity x priority grid layout used by the incident detail
// page's NIST matrix (read-only, click-to-set) and Settings -> Incident SLAs
// (editable inputs) -- same row/column structure, different cell content, so
// only renderCell varies between the two.
export function SeverityPriorityGrid({
  renderCell,
}: {
  renderCell: (severity: Severity, priority: IncidentPriority) => ReactNode;
}) {
  const { t } = useTranslation();

  return (
    <div className="nist-matrix">
      <span />
      {PRIORITY_ORDER.map((p) => (
        <span className="nist-matrix-header-cell" key={p}>
          {p.toUpperCase()}
        </span>
      ))}
      {SEVERITY_PRIORITY_GRID_SEVERITIES.map((sev) => (
        <Fragment key={sev}>
          <span className="nist-matrix-row-label">{t(`common.severity.${sev}`)}</span>
          {PRIORITY_ORDER.map((p) => (
            <Fragment key={`${sev}-${p}`}>{renderCell(sev, p)}</Fragment>
          ))}
        </Fragment>
      ))}
    </div>
  );
}
