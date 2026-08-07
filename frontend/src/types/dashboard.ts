import type { Alert } from "./alerts";
import type { Incident } from "./incidents";

// Mirrors domain.AlertTrendPoint -- one day of mv_alert_daily_stats, for the
// Dashboard Alerts tab's trend chart.
export interface AlertTrendPoint {
  day: string;
  alertCount: number;
  avgMttrSeconds?: number;
}

// Mirrors domain.ActivityEvent -- one row of the Dashboard's Recent Activity
// feed, a union of alert_events and incident_events.
export interface ActivityEvent {
  kind: "alert" | "incident";
  contextId: string;
  contextTitle: string;
  eventType: string;
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
  alertsBySeverity: Record<string, number>;
  alertStatusDistribution: Record<string, number>;
  incidentsByPriority: Record<string, number>;
  incidentsByPhase: Record<string, number>;
}

// Mirrors domain.FollowupView -- GET /api/v1/dashboard/followup, gated by
// the "followup" capability independently of "alerts"/"incidents" (see
// AuthContext.hasResourceAccess).
export interface FollowupView {
  alerts: Alert[];
  incidents: Incident[];
}
