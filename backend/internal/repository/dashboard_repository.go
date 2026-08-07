package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type DashboardRepository struct{}

func NewDashboardRepository() *DashboardRepository {
	return &DashboardRepository{}
}

// StatsFilter narrows Stats to the Dashboard's Alerts-tab or Incidents-tab
// filter bar (see the design handoff screenshots) -- Alert* fields apply to
// every alert-derived figure (open/critical counts, severity/status
// breakdowns), Incident* fields apply to every incident-derived one
// (active/SLA/P1 counts, priority/phase breakdowns). They're independent
// (distinct query param names on the handler side) because both tabs share
// one /dashboard/stats endpoint but filter different aggregates. AlertTrend
// is never filtered -- mv_alert_daily_stats is pre-aggregated by day only,
// with no severity/status/source/tag dimension to filter on.
type StatsFilter struct {
	AlertSeverity *domain.Severity
	AlertStatus   *domain.AlertStatus
	AlertSource   *string
	AlertTag      *string

	IncidentSeverity *domain.Severity
	IncidentTag      *string

	// AllowedTags scopes every figure below (live counts, breakdowns) to the
	// caller's tag-based access -- same "empty means unrestricted" semantics
	// as ListAlertsFilter.AllowedTags/ListIncidentsFilter.AllowedTags. Set
	// from middleware.AllowedTags(ctx) by the handler, not user-suppliable.
	// Applies to both the alert-derived and incident-derived aggregates,
	// matching how DashboardService.Followup already applies one allowedTags
	// slice across both.
	AllowedTags []string
}

// Stats assembles domain.DashboardStats from two sources: a live COUNT(*)
// against `alerts`/`incidents` (RLS-scoped by the caller's tenant-scoped
// tx, same as every other repository) for every count-based figure, and
// the two materialized views only for genuine average-over-time metrics
// (MTTA/MTTR). Active/SLA-breached/P1 incident counts used to read from
// mv_incident_kpis, which cmd/worker only refreshes once a minute -- a
// freshly created or closed incident wouldn't show up on the Dashboard
// for up to 60s, which read as "the incident didn't reflect" to a user
// testing it live. Counts are cheap to compute live (same reasoning the
// alert counts above already used), so only the two *_seconds averages
// stay materialized-view-backed.
// mv_alert_daily_stats / mv_incident_kpis carry no RLS (materialized views
// can't), so that query filters tenant_id explicitly -- see the warning in
// cmd/worker/main.go's refreshMaterializedViews.
func (r *DashboardRepository) Stats(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, filter StatsFilter) (*domain.DashboardStats, error) {
	stats := &domain.DashboardStats{}

	alertWhere, alertArgs := alertFilterClause(filter)
	if err := tx.QueryRow(ctx, `
		select
			count(*) filter (where status <> 'closed'),
			count(*) filter (where status <> 'closed' and severity = 'critical'),
			count(*) filter (where status <> 'closed' and severity = 'high')
		from alerts`+alertWhere,
		alertArgs...,
	).Scan(&stats.OpenAlerts, &stats.CriticalAlerts, &stats.HighAlerts); err != nil {
		return nil, fmt.Errorf("live alert counts: %w", err)
	}

	incidentWhere, incidentArgs := incidentFilterClause(filter)
	if err := tx.QueryRow(ctx, `
		select
			count(*) filter (where phase <> 'post_incident'),
			count(*) filter (where sla_breached),
			count(*) filter (where priority = 'p1' and phase <> 'post_incident')
		from incidents`+incidentWhere,
		incidentArgs...,
	).Scan(&stats.ActiveIncidents, &stats.SLABreachedCount, &stats.P1OpenCount); err != nil {
		return nil, fmt.Errorf("live incident counts: %w", err)
	}

	// mv_incident_kpis has at most one row per tenant, and none at all for a
	// tenant with zero incidents (it's a plain `group by tenant_id` over
	// `incidents`, which produces no row when there's nothing to group).
	// Absence just means "no average yet", not an error.
	err := tx.QueryRow(ctx, `
		select avg_mtta_seconds, avg_mttr_seconds
		from mv_incident_kpis
		where tenant_id = $1`,
		tenantID,
	).Scan(&stats.IncidentAvgMTTASeconds, &stats.IncidentAvgMTTRSeconds)
	if err != nil && err != pgx.ErrNoRows {
		return nil, fmt.Errorf("incident kpis: %w", err)
	}

	// mv_alert_daily_stats is one row per (tenant, day); roll the last 30
	// days up into a single trend figure for the dashboard card.
	err = tx.QueryRow(ctx, `
		select avg(avg_mtta_seconds), avg(avg_mttr_seconds)
		from mv_alert_daily_stats
		where tenant_id = $1 and day >= now() - interval '30 days'`,
		tenantID,
	).Scan(&stats.AlertAvgMTTASeconds, &stats.AlertAvgMTTRSeconds)
	if err != nil && err != pgx.ErrNoRows {
		return nil, fmt.Errorf("alert daily stats: %w", err)
	}

	trend, err := r.alertTrend(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	stats.AlertTrend = trend

	if stats.AlertsBySeverity, err = countGroupedBy(ctx, tx, "alerts", "severity", alertWhere, alertArgs); err != nil {
		return nil, fmt.Errorf("alerts by severity: %w", err)
	}
	if stats.AlertStatusDistribution, err = countGroupedBy(ctx, tx, "alerts", "status", alertWhere, alertArgs); err != nil {
		return nil, fmt.Errorf("alert status distribution: %w", err)
	}
	if stats.IncidentsByPriority, err = countGroupedBy(ctx, tx, "incidents", "priority", incidentWhere, incidentArgs); err != nil {
		return nil, fmt.Errorf("incidents by priority: %w", err)
	}
	if stats.IncidentsByPhase, err = countGroupedBy(ctx, tx, "incidents", "phase", incidentWhere, incidentArgs); err != nil {
		return nil, fmt.Errorf("incidents by phase: %w", err)
	}

	return stats, nil
}

// alertFilterClause/incidentFilterClause build a `where ...` fragment (or
// "" for no filter) plus its args, shared across every alert-derived or
// incident-derived query in Stats -- see StatsFilter's doc comment for why
// these are independent of each other.
func alertFilterClause(f StatsFilter) (string, []any) {
	var clauses []string
	var args []any
	if f.AlertSeverity != nil {
		args = append(args, *f.AlertSeverity)
		clauses = append(clauses, fmt.Sprintf("severity = $%d", len(args)))
	}
	if f.AlertStatus != nil {
		args = append(args, *f.AlertStatus)
		clauses = append(clauses, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.AlertSource != nil {
		args = append(args, *f.AlertSource)
		clauses = append(clauses, fmt.Sprintf("source = $%d", len(args)))
	}
	if f.AlertTag != nil {
		args = append(args, *f.AlertTag)
		clauses = append(clauses, fmt.Sprintf("$%d = any(tags)", len(args)))
	}
	if len(f.AllowedTags) > 0 {
		args = append(args, f.AllowedTags)
		clauses = append(clauses, fmt.Sprintf("tags && $%d", len(args)))
	}
	return whereClause(clauses), args
}

func incidentFilterClause(f StatsFilter) (string, []any) {
	var clauses []string
	var args []any
	if f.IncidentSeverity != nil {
		args = append(args, *f.IncidentSeverity)
		clauses = append(clauses, fmt.Sprintf("severity = $%d", len(args)))
	}
	if f.IncidentTag != nil {
		args = append(args, *f.IncidentTag)
		clauses = append(clauses, fmt.Sprintf("$%d = any(tags)", len(args)))
	}
	if len(f.AllowedTags) > 0 {
		args = append(args, f.AllowedTags)
		clauses = append(clauses, fmt.Sprintf("tags && $%d", len(args)))
	}
	return whereClause(clauses), args
}

func whereClause(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	out := " where "
	for i, c := range clauses {
		if i > 0 {
			out += " and "
		}
		out += c
	}
	return out
}

// alertTrend returns the last 14 days of mv_alert_daily_stats, oldest
// first, zero-filled for a fresh tenant with no history yet. Not scoped by
// AllowedTags: mv_alert_daily_stats is pre-aggregated by (tenant_id, day)
// only and carries no tags column, so per-tag filtering isn't possible
// without restructuring the materialized view -- out of scope here. The
// trend chart's day counts/MTTR figures stay tenant-wide; the KPI cards,
// breakdown charts, and activity feed (the actual reported leak) are scoped.
func (r *DashboardRepository) alertTrend(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]domain.AlertTrendPoint, error) {
	rows, err := tx.Query(ctx, `
		select to_char(day, 'YYYY-MM-DD'), alert_count, avg_mttr_seconds
		from mv_alert_daily_stats
		where tenant_id = $1 and day >= now() - interval '14 days'
		order by day asc`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("alert trend: %w", err)
	}
	defer rows.Close()

	points := []domain.AlertTrendPoint{}
	for rows.Next() {
		var p domain.AlertTrendPoint
		if err := rows.Scan(&p.Day, &p.AlertCount, &p.AvgMTTRSeconds); err != nil {
			return nil, fmt.Errorf("scan alert trend point: %w", err)
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// countGroupedBy is a small helper for the "distribution across every row
// matching the current filter" breakdown charts (severity, status,
// priority, phase) -- table/column are always call-site constants (never
// user-supplied), so building the query with fmt.Sprintf here is safe; the
// where/args pair came from alertFilterClause/incidentFilterClause, which
// already parameterize the one truly user-supplied part.
func countGroupedBy(ctx context.Context, tx pgx.Tx, table, column, where string, args []any) (map[string]int, error) {
	rows, err := tx.Query(ctx, fmt.Sprintf(`select %s, count(*) from %s%s group by %s`, column, table, where, column), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		counts[key] = n
	}
	return counts, rows.Err()
}

// RecentActivity is the Dashboard's Recent Activity feed: a union of
// alert_events, incident_events, and the incident_comments/alert_comments
// tables (each synthesized as a "comment_added" event_type -- comments have
// no event_type column of their own since they're not part of the
// append-only *_events audit log). All tables are RLS-scoped by tx the same
// as every other query here, so no explicit tenant filter is needed (unlike
// the materialized-view queries above). Newest first.
//
// kind narrows the feed to "alert" or "incident" only (the Alerts dashboard
// tab must never show incident activity and vice versa); "" means no
// filter. allowedTags scopes every branch to the caller's tag-based access
// (empty = unrestricted), same semantics as StatsFilter.AllowedTags. Each
// UNION leg is a plain SELECT, so a WHERE clause can't scope individual
// legs -- the branches are built conditionally in Go instead, and the tags
// clause is appended to each one that needs it.
func (r *DashboardRepository) RecentActivity(ctx context.Context, tx pgx.Tx, limit int, kind string, allowedTags []string) ([]domain.ActivityEvent, error) {
	var branches []string
	var args []any
	tagsIdx := 0
	if len(allowedTags) > 0 {
		args = append(args, allowedTags)
		tagsIdx = len(args)
	}
	tagFilter := func(alias string) string {
		if tagsIdx == 0 {
			return ""
		}
		return fmt.Sprintf(" where %s.tags && $%d", alias, tagsIdx)
	}

	if kind == "" || kind == "alert" {
		branches = append(branches, `
			select 'alert' as kind, a.id, a.title, ae.event_type, ae.actor_type, ae.actor_id, ae.data, ae.created_at
			from alert_events ae join alerts a on a.id = ae.alert_id`+tagFilter("a"))
		branches = append(branches, `
			select 'alert' as kind, a.id, a.title, 'comment_added', 'user',
			       c.author_id, jsonb_build_object('authorName', c.author_name)::jsonb, c.created_at
			from alert_comments c join alerts a on a.id = c.alert_id`+tagFilter("a"))
	}
	if kind == "" || kind == "incident" {
		branches = append(branches, `
			select 'incident' as kind, i.id, i.title, ie.event_type, ie.actor_type, ie.actor_id, ie.data, ie.created_at
			from incident_events ie join incidents i on i.id = ie.incident_id`+tagFilter("i"))
		branches = append(branches, `
			select 'incident' as kind, i.id, i.title, 'comment_added', 'user',
			       c.author_id, jsonb_build_object('authorName', c.author_name)::jsonb, c.created_at
			from incident_comments c join incidents i on i.id = c.incident_id`+tagFilter("i"))
	}
	if len(branches) == 0 {
		return []domain.ActivityEvent{}, nil
	}

	args = append(args, limit)
	query := strings.Join(branches, " union all ") + fmt.Sprintf(" order by created_at desc limit $%d", len(args))
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query recent activity: %w", err)
	}
	defer rows.Close()

	events := []domain.ActivityEvent{}
	for rows.Next() {
		var e domain.ActivityEvent
		if err := rows.Scan(&e.Kind, &e.ContextID, &e.ContextTitle, &e.EventType, &e.ActorType, &e.ActorID, &e.Data, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan activity event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
