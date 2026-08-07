import { useTranslation } from "react-i18next";
import type { Severity, AlertStatus, Classification } from "../types/alerts";
import type { IncidentPhase, IncidentPriority } from "../types/incidents";

const SEVERITY_CLASS: Record<Severity, string> = {
  critical: "badge-sev-critical",
  high: "badge-sev-high",
  medium: "badge-sev-medium",
  low: "badge-sev-low",
  informational: "badge-sev-info",
};

export function SeverityBadge({ severity }: { severity: Severity }) {
  const { t } = useTranslation();
  return (
    <span className={`badge badge-severity ${SEVERITY_CLASS[severity]}`}>
      <span className="badge-status-dot" />
      {t(`common.severity.${severity}`)}
    </span>
  );
}

const ALERT_STATUS_CLASS: Record<AlertStatus, string> = {
  open: "badge-status-open",
  investigating: "badge-status-investigating",
  escalated: "badge-status-escalated",
  closed: "badge-status-closed",
};

export function AlertStatusBadge({ status }: { status: AlertStatus }) {
  const { t } = useTranslation();
  return <span className={`badge badge-severity ${ALERT_STATUS_CLASS[status]}`}>{t(`common.alertStatus.${status}`)}</span>;
}

export function ClassificationBadge({ classification }: { classification: Classification }) {
  const { t } = useTranslation();
  return <span className="badge badge-muted">{t(`common.classification.${classification}`)}</span>;
}

export function PriorityBadge({ priority }: { priority: IncidentPriority }) {
  return <span className="badge badge-priority mono">{priority.toUpperCase()}</span>;
}

const PHASE_CLASS: Record<IncidentPhase, string> = {
  new: "badge-phase-new",
  detection_analysis: "badge-phase-detection_analysis",
  containment: "badge-phase-containment",
  eradication: "badge-phase-eradication",
  recovery: "badge-phase-recovery",
  post_incident: "badge-phase-post_incident",
};

export function PhasePill({ phase }: { phase: IncidentPhase }) {
  const { t } = useTranslation();
  return <span className={`badge ${PHASE_CLASS[phase]}`}>{t(`common.phase.${phase}`)}</span>;
}
