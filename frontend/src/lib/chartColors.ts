import type { Severity, AlertStatus } from "../types/alerts";
import type { IncidentPhase, IncidentPriority } from "../types/incidents";

// Central place for chart/breakdown colors, all pointing at the same CSS
// custom properties the badge components already use (styles/tokens.css) --
// keeps a severity/status/phase reading the same color everywhere in the
// app, whether it's a badge or a bar in a chart.
export const SEVERITY_ORDER: Severity[] = ["critical", "high", "medium", "low", "informational"];
export const SEVERITY_COLOR: Record<Severity, string> = {
  critical: "var(--critical)",
  high: "var(--high)",
  medium: "var(--medium)",
  low: "var(--low)",
  informational: "var(--info)",
};

export const ALERT_STATUS_ORDER: AlertStatus[] = ["open", "investigating", "escalated", "closed"];
export const ALERT_STATUS_COLOR: Record<AlertStatus, string> = {
  open: "var(--critical)",
  investigating: "var(--high)",
  escalated: "var(--medium)",
  closed: "var(--info)",
};

export const PRIORITY_ORDER: IncidentPriority[] = ["p1", "p2", "p3", "p4"];

export const PHASE_ORDER: IncidentPhase[] = [
  "new",
  "detection_analysis",
  "containment",
  "eradication",
  "recovery",
  "post_incident",
];
export const PHASE_COLOR: Record<IncidentPhase, string> = {
  new: "var(--info)",
  detection_analysis: "var(--high)",
  containment: "var(--critical)",
  eradication: "var(--high)",
  recovery: "var(--success)",
  post_incident: "var(--info)",
};
