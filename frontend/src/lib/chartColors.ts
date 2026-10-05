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
// investigating deliberately reuses low's teal rather than high's orange --
// orange (high) and yellow (escalated/medium) sit right next to each other
// on the donut/legend and read as nearly the same color at a glance. Open
// stays critical-red (it's the most urgent state) and escalated stays
// medium-yellow; investigating is the one that needed to move.
export const ALERT_STATUS_COLOR: Record<AlertStatus, string> = {
  open: "var(--critical)",
  investigating: "var(--low)",
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

// severityFilterOptions/alertStatusFilterOptions/phaseFilterOptions build
// MultiSelectFilter's options array with each value's color already
// attached, from the same *_ORDER/*_COLOR pairs above -- every
// severity/status/phase filter in the app (dashboard tabs, the Alerts/
// Incidents list pages) shares one source of truth for both, instead of a
// hand-written options array per call site that could drift from the badge
// colors it's supposed to match.
export function severityFilterOptions(t: (key: string) => string): { value: Severity; label: string; color: string }[] {
  return SEVERITY_ORDER.map((s) => ({ value: s, label: t(`common.severity.${s}`), color: SEVERITY_COLOR[s] }));
}

export function alertStatusFilterOptions(t: (key: string) => string): { value: AlertStatus; label: string; color: string }[] {
  return ALERT_STATUS_ORDER.map((s) => ({ value: s, label: t(`common.alertStatus.${s}`), color: ALERT_STATUS_COLOR[s] }));
}

export function phaseFilterOptions(t: (key: string) => string): { value: IncidentPhase; label: string; color: string }[] {
  return PHASE_ORDER.map((p) => ({ value: p, label: t(`common.phase.${p}`), color: PHASE_COLOR[p] }));
}
