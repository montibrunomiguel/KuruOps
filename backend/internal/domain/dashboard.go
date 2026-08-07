package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AlertTrendPoint is one day of mv_alert_daily_stats, exposed for the
// Dashboard's Alerts-tab trend chart -- see DashboardRepository.Stats for
// why day-level granularity comes from the materialized view rather than a
// live query.
type AlertTrendPoint struct {
	Day            string   `json:"day"`
	AlertCount     int      `json:"alertCount"`
	AvgMTTRSeconds *float64 `json:"avgMttrSeconds,omitempty"`
}

// ActivityEvent is one row of the Dashboard's Recent Activity feed -- a
// union of alert_events and incident_events, newest first. Kind/ContextID
// tell the frontend which detail page a click should navigate to.
type ActivityEvent struct {
	Kind         string          `json:"kind"` // "alert" or "incident"
	ContextID    uuid.UUID       `json:"contextId"`
	ContextTitle string          `json:"contextTitle"`
	EventType    string          `json:"eventType"`
	ActorType    ActorType       `json:"actorType"`
	ActorID      *uuid.UUID      `json:"actorId,omitempty"`
	Data         json.RawMessage `json:"data"`
	CreatedAt    time.Time       `json:"createdAt"`
}

// DashboardStats is computed server-side for the Dashboard's KPI cards --
// "live" counts (open/critical alerts) come from a cheap indexed COUNT(*)
// against the alerts table so they're always current; MTTA/MTTR and the
// incident KPIs come from mv_alert_daily_stats / mv_incident_kpis, which
// cmd/worker refreshes on a schedule (see db/migrations/0009_materialized_views.up.sql).
// The trade-off is deliberate: an average-over-time metric doesn't need
// per-request freshness, and computing it from a materialized view instead
// of fetching every alert/incident row into the client is what actually
// lets this scale past mock-data volumes.
type DashboardStats struct {
	OpenAlerts       int `json:"openAlerts"`
	CriticalAlerts   int `json:"criticalAlerts"`
	HighAlerts       int `json:"highAlerts"`
	ActiveIncidents  int `json:"activeIncidents"`
	SLABreachedCount int `json:"slaBreachedCount"`
	P1OpenCount      int `json:"p1OpenCount"`

	// Averages are nil when there isn't enough closed/acknowledged history
	// yet to compute one (e.g. a fresh tenant with no closed alerts).
	IncidentAvgMTTASeconds *float64 `json:"incidentAvgMttaSeconds,omitempty"`
	IncidentAvgMTTRSeconds *float64 `json:"incidentAvgMttrSeconds,omitempty"`
	AlertAvgMTTASeconds    *float64 `json:"alertAvgMttaSeconds,omitempty"`
	AlertAvgMTTRSeconds    *float64 `json:"alertAvgMttrSeconds,omitempty"`

	// AlertTrend is the last 14 days of mv_alert_daily_stats, oldest first --
	// the Dashboard Alerts tab's trend chart.
	AlertTrend []AlertTrendPoint `json:"alertTrend"`
	// AlertsBySeverity/AlertStatusDistribution are live counts across every
	// alert regardless of status/age -- an overall-distribution snapshot,
	// not a "currently open" one (that's OpenAlerts/CriticalAlerts above).
	AlertsBySeverity        map[string]int `json:"alertsBySeverity"`
	AlertStatusDistribution map[string]int `json:"alertStatusDistribution"`
	// IncidentsByPriority/IncidentsByPhase are the same kind of
	// live full-history breakdown, for incidents.
	IncidentsByPriority map[string]int `json:"incidentsByPriority"`
	IncidentsByPhase    map[string]int `json:"incidentsByPhase"`
}

// FollowupView is the Dashboard's Follow-up tab: alerts and incidents that
// need a decision, gated by ResourceCapabilityFollowup independently of the
// general Alerts/Incidents sections (see middleware.RequireResourceAccess).
type FollowupView struct {
	Alerts    []Alert    `json:"alerts"`
	Incidents []Incident `json:"incidents"`
}
