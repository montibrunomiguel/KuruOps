import type { Alert, AlertEventType } from "./alerts";
import type { Incident, IncidentEventType } from "./incidents";

// Mirrors domain.AlertTrendPoint -- one day of mv_alert_daily_stats, for the
// Dashboard Alerts tab's trend chart.
export interface AlertTrendPoint {
  day: string;
  alertCount: number;
  avgMttrSeconds?: number;
}

// Mirrors domain.IncidentTrendPoint -- one day of mv_incident_daily_stats,
// for the Dashboard Incidents tab's volume chart.
export interface IncidentTrendPoint {
  day: string;
  incidentCount: number;
}

// "comment_added" isn't a real AlertEventType/IncidentEventType constant --
// the dashboard repository's activity-feed query synthesizes it with that
// literal string when it UNIONs in the comments tables alongside the real
// alert_events/incident_events log (see dashboard_repository.go).
export type ActivityEventType = AlertEventType | IncidentEventType | "comment_added";

// Mirrors domain.ActivityEvent -- one row of the Dashboard's Recent Activity
// feed, a union of alert_events and incident_events.
export interface ActivityEvent {
  kind: "alert" | "incident";
  contextId: string;
  contextTitle: string;
  eventType: ActivityEventType;
  actorType: "user" | "system" | "ai";
  actorId?: string;
  data: unknown;
  createdAt: string;
}

// Mirrors backend/internal/domain/dashboard.go. Computed server-side (live
// COUNT for the alert numbers, materialized views for the MTTA/MTTR
// averages, live GROUP BY for the breakdown maps) instead of derived by
// summing full alert/incident lists in the browser -- see
// GET /api/v1/dashboard/stats.
export interface DashboardStats {
  openAlerts: number;
  criticalAlerts: number;
  highAlerts: number;
  activeIncidents: number;
  slaBreachedCount: number;
  p1OpenCount: number;
  incidentAvgMttaSeconds?: number;
  incidentAvgMttrSeconds?: number;
  alertAvgMttaSeconds?: number;
  alertAvgMttrSeconds?: number;

  alertTrend: AlertTrendPoint[];
  incidentTrend: IncidentTrendPoint[];
  alertsBySeverity: Record<string, number>;
  alertStatusDistribution: Record<string, number>;
  incidentsByPriority: Record<string, number>;
  incidentsByPhase: Record<string, number>;
  // Identity-keyed breakdowns (see backend domain.NamedCount) -- id is
  // absent for the "unassigned"/"no commander yet" bucket, whose name the
  // frontend renders itself rather than trusting a server-supplied label.
  alertsByAnalyst: NamedCount[];
  incidentsByCommander: NamedCount[];
}

export interface NamedCount {
  id?: string;
  name: string;
  count: number;
}

// Mirrors domain.FollowupView -- GET /api/v1/dashboard/followup, gated by
// the "followup" capability independently of "alerts"/"incidents" (see
// AuthContext.hasResourceAccess).
export interface FollowupView {
  alerts: Alert[];
  incidents: Incident[];
}
