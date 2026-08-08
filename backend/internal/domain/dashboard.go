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

// IncidentTrendPoint is one day of mv_incident_daily_stats, exposed for the
// Dashboard's Incidents-tab volume chart -- the incident-side counterpart of
// AlertTrendPoint. No MTTR figure: mv_incident_kpis' MTTA/MTTR averages are
// already tenant-wide (see DashboardStats.IncidentAvgMTTASeconds), and
// there's no per-day breakdown of them requested here.
type IncidentTrendPoint struct {
	Day           string `json:"day"`
	IncidentCount int    `json:"incidentCount"`
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

// NamedCount is a breakdown row keyed by a person's identity rather than a
// fixed enum value (unlike AlertsBySeverity/IncidentsByPhase's map[string]int,
// which is safe to key by the enum string itself) -- two different analysts
// can share a display name, and "unassigned"/"no commander yet" needs its
// own bucket, so ID (nil for that bucket) is what actually disambiguates.
type NamedCount struct {
	ID    *uuid.UUID `json:"id,omitempty"`
	Name  string     `json:"name"`
	Count int        `json:"count"`
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
	// IncidentTrend is the last 14 days of mv_incident_daily_stats, oldest
	// first -- the Dashboard Incidents tab's volume chart.
	IncidentTrend []IncidentTrendPoint `json:"incidentTrend"`
	// AlertsBySeverity/AlertStatusDistribution are live counts across every
	// alert regardless of status/age -- an overall-distribution snapshot,
	// not a "currently open" one (that's OpenAlerts/CriticalAlerts above).
	AlertsBySeverity        map[string]int `json:"alertsBySeverity"`
	AlertStatusDistribution map[string]int `json:"alertStatusDistribution"`
	// IncidentsByPriority/IncidentsByPhase are the same kind of
	// live full-history breakdown, for incidents.
	IncidentsByPriority map[string]int `json:"incidentsByPriority"`
	IncidentsByPhase    map[string]int `json:"incidentsByPhase"`
	// AlertsByAnalyst/IncidentsByCommander are identity-keyed breakdowns
	// (see NamedCount) -- one row per assigned_analyst_id / per-incident
	// 'commander' role assignment, most recent-history-wide like the other
	// breakdowns above. A nil-ID row is the "unassigned"/"no commander yet"
	// bucket.
	AlertsByAnalyst      []NamedCount `json:"alertsByAnalyst"`
	IncidentsByCommander []NamedCount `json:"incidentsByCommander"`
}

// FollowupView is the Dashboard's Follow-up tab: alerts and incidents that
// need a decision, gated by ResourceCapabilityFollowup independently of the
// general Alerts/Incidents sections (see middleware.RequireResourceAccess).
type FollowupView struct {
	Alerts    []Alert    `json:"alerts"`
	Incidents []Incident `json:"incidents"`
}
