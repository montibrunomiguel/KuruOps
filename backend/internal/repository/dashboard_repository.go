package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
// Every user-facing filter field below is a slice, not a single pointer --
// an empty/nil slice means "no filter" (same convention AllowedTags already
// used), one or more values means "any of these" (OR within the field, same
// "= any($n)"/array-overlap technique AllowedTags already applied server-
// side, now also driving the Dashboard's own multi-select filter bar).
type StatsFilter struct {
	AlertSeverity []domain.Severity
	AlertStatus   []domain.AlertStatus
	AlertSource   *string
	AlertTag      []string
	// AssignedAnalystID narrows every alert-derived figure to any of these
	// analysts' alerts -- the Alerts tab's analyst filter.
	AssignedAnalystID []uuid.UUID

	IncidentSeverity []domain.Severity
	IncidentTag      []string
	// CommanderID narrows every incident-derived figure to incidents where
	// one of these users holds the 'commander' role (see
	// incident_role_assignments) -- the Incidents tab's commander filter.
	CommanderID []uuid.UUID

	// Since/Until restrict every count/breakdown below to alerts received (or
	// incidents opened) within [Since, Until] -- the Dashboard's time-range
	// filter, either endpoint optional. Applied to the same alert/incident-
	// derived figures AllowedTags scopes; AlertTrend/IncidentTrend/the 30-day
	// MTTA-MTTR average stay on their own fixed lookback windows regardless
	// (see alertTrend's doc comment -- the materialized views they read from
	// have no per-request-filterable dimension without restructuring the
	// view itself).
	Since *time.Time
	Until *time.Time

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
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
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
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("alert daily stats: %w", err)
	}

	trend, err := r.alertTrend(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	stats.AlertTrend = trend

	incidentTrend, err := r.incidentTrend(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	stats.IncidentTrend = incidentTrend

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
	if stats.AlertsByAnalyst, err = r.alertsByAnalyst(ctx, tx, alertWhere, alertArgs); err != nil {
		return nil, fmt.Errorf("alerts by analyst: %w", err)
	}
	if stats.IncidentsByCommander, err = r.incidentsByCommander(ctx, tx, incidentWhere, incidentArgs); err != nil {
		return nil, fmt.Errorf("incidents by commander: %w", err)
	}

	return stats, nil
}

// alertsByAnalyst groups every alert matching where/args by
// assigned_analyst_id, joined to the analyst's current name -- an
// unassigned alert (assigned_analyst_id is null) groups into its own row
// with Name left "" so the frontend renders its own localized "Unassigned"
// label rather than a hardcoded English string coming from the backend.
func (r *DashboardRepository) alertsByAnalyst(ctx context.Context, tx pgx.Tx, where string, args []any) ([]domain.NamedCount, error) {
	query := `
		select a.assigned_analyst_id, coalesce(u.name, ''), count(*)
		from alerts a left join users u on u.id = a.assigned_analyst_id` + where + `
		group by a.assigned_analyst_id, u.name
		order by count(*) desc`
	return queryList(ctx, tx, query, scanNamedCount, args...)
}

// incidentsByCommander is alertsByAnalyst's incident-side counterpart --
// but unlike assigned_analyst_id, there's no commander_id column on
// incidents to group by directly (it's normalized into
// incident_role_assignments, see that table's migration), so this joins
// through it instead. An incident with no commander assigned yet groups
// into the same nil-ID/empty-Name "unassigned" bucket. The grouping itself
// happens in an inner subquery scoped to just incidents+incident_role_assignments
// (where's bare column references, e.g. CommanderID's own "incidents.id in
// (...)" clause, are unambiguous) -- users is only joined in the outer
// query, purely for the display name, specifically so introducing it never
// makes a bare "id" reference ambiguous inside where.
func (r *DashboardRepository) incidentsByCommander(ctx context.Context, tx pgx.Tx, where string, args []any) ([]domain.NamedCount, error) {
	query := `
		select grouped.commander_id, coalesce(u.name, ''), grouped.cnt
		from (
			select ra.user_id as commander_id, count(*) as cnt
			from incidents
			left join incident_role_assignments ra on ra.incident_id = incidents.id and ra.role = 'commander'` + where + `
			group by ra.user_id
		) grouped
		left join users u on u.id = grouped.commander_id
		order by grouped.cnt desc`
	return queryList(ctx, tx, query, scanNamedCount, args...)
}

func scanNamedCount(row pgx.Row) (*domain.NamedCount, error) {
	var c domain.NamedCount
	if err := row.Scan(&c.ID, &c.Name, &c.Count); err != nil {
		return nil, fmt.Errorf("scan named count: %w", err)
	}
	return &c, nil
}

// alertFilterClause/incidentFilterClause build a `where ...` fragment (or
// "" for no filter) plus its args, shared across every alert-derived or
// incident-derived query in Stats -- see StatsFilter's doc comment for why
// these are independent of each other.
func alertFilterClause(f StatsFilter) (string, []any) {
	var clauses []string
	var args []any
	if len(f.AlertSeverity) > 0 {
		args = append(args, toStrings(f.AlertSeverity))
		// severity is severity_enum (see db/migrations/0001_initial_schema.up.sql), not text --
		// casting both sides to text sidesteps Postgres needing to resolve
		// $n's element type against the enum on its own, which it can't do
		// implicitly for an any(array) comparison the way it can for a
		// plain `severity = 'critical'` scalar comparison.
		clauses = append(clauses, fmt.Sprintf("severity::text = any($%d::text[])", len(args)))
	}
	if len(f.AlertStatus) > 0 {
		args = append(args, toStrings(f.AlertStatus))
		clauses = append(clauses, fmt.Sprintf("status::text = any($%d::text[])", len(args)))
	}
	if f.AlertSource != nil {
		args = append(args, *f.AlertSource)
		clauses = append(clauses, fmt.Sprintf("source = $%d", len(args)))
	}
	if len(f.AlertTag) > 0 {
		args = append(args, f.AlertTag)
		clauses = append(clauses, fmt.Sprintf("tags && $%d", len(args)))
	}
	if f.Since != nil {
		args = append(args, *f.Since)
		clauses = append(clauses, fmt.Sprintf("received_at >= $%d", len(args)))
	}
	if f.Until != nil {
		args = append(args, *f.Until)
		clauses = append(clauses, fmt.Sprintf("received_at <= $%d", len(args)))
	}
	if len(f.AssignedAnalystID) > 0 {
		args = append(args, f.AssignedAnalystID)
		clauses = append(clauses, fmt.Sprintf("assigned_analyst_id = any($%d)", len(args)))
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
	if len(f.IncidentSeverity) > 0 {
		args = append(args, toStrings(f.IncidentSeverity))
		clauses = append(clauses, fmt.Sprintf("severity::text = any($%d::text[])", len(args)))
	}
	if len(f.IncidentTag) > 0 {
		args = append(args, f.IncidentTag)
		clauses = append(clauses, fmt.Sprintf("tags && $%d", len(args)))
	}
	if f.Since != nil {
		args = append(args, *f.Since)
		clauses = append(clauses, fmt.Sprintf("opened_at >= $%d", len(args)))
	}
	if f.Until != nil {
		args = append(args, *f.Until)
		clauses = append(clauses, fmt.Sprintf("opened_at <= $%d", len(args)))
	}
	if len(f.CommanderID) > 0 {
		args = append(args, f.CommanderID)
		// incidents.id (table-qualified, not "id" bare) so this stays valid
		// both against plain `from incidents` call sites and against
		// incidentsByCommander's own incidents-plus-incident_role_assignments
		// join, where a bare "id" would be ambiguous the moment users (which
		// also has an id column) enters the same query scope.
		clauses = append(clauses, fmt.Sprintf(
			"incidents.id in (select incident_id from incident_role_assignments where role = 'commander' and user_id = any($%d))",
			len(args),
		))
	}
	if len(f.AllowedTags) > 0 {
		args = append(args, f.AllowedTags)
		clauses = append(clauses, fmt.Sprintf("tags && $%d", len(args)))
	}
	return whereClause(clauses), args
}

// toStrings converts a slice of a named string type (domain.Severity,
// domain.AlertStatus) to plain []string before it's passed as a query arg
// -- pgx's array codec resolves cleanly for []string (same type
// AllowedTags already uses), so this sidesteps needing to confirm it
// handles an arbitrary named-string element type as reliably.
func toStrings[T ~string](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
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
	return queryList(ctx, tx, `
		select to_char(day, 'YYYY-MM-DD'), alert_count, avg_mttr_seconds
		from mv_alert_daily_stats
		where tenant_id = $1 and day >= now() - interval '14 days'
		order by day asc`,
		func(row pgx.Row) (*domain.AlertTrendPoint, error) {
			var p domain.AlertTrendPoint
			if err := row.Scan(&p.Day, &p.AlertCount, &p.AvgMTTRSeconds); err != nil {
				return nil, fmt.Errorf("scan alert trend point: %w", err)
			}
			return &p, nil
		},
		tenantID,
	)
}

// incidentTrend is alertTrend's incident-side counterpart, reading from
// mv_incident_daily_stats instead -- see IncidentTrendPoint's doc comment
// for why there's no MTTR figure alongside the count.
func (r *DashboardRepository) incidentTrend(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]domain.IncidentTrendPoint, error) {
	return queryList(ctx, tx, `
		select to_char(day, 'YYYY-MM-DD'), incident_count
		from mv_incident_daily_stats
		where tenant_id = $1 and day >= now() - interval '14 days'
		order by day asc`,
		func(row pgx.Row) (*domain.IncidentTrendPoint, error) {
			var p domain.IncidentTrendPoint
			if err := row.Scan(&p.Day, &p.IncidentCount); err != nil {
				return nil, fmt.Errorf("scan incident trend point: %w", err)
			}
			return &p, nil
		},
		tenantID,
	)
}

// countGroupedByAllowedTables/countGroupedByAllowedColumns are the only
// values countGroupedBy will ever interpolate into a query -- table/column
// are always call-site constants today (never user-supplied), but this
// allowlist means a future caller can't accidentally turn that into a SQL
// injection vector by passing something derived from a request. Kept as an
// explicit list (not just "trust the caller") specifically because this is
// the one place in the repository layer that builds a query with
// fmt.Sprintf instead of full parameterization.
var (
	countGroupedByAllowedTables  = map[string]bool{"alerts": true, "incidents": true}
	countGroupedByAllowedColumns = map[string]bool{"severity": true, "status": true, "priority": true, "phase": true}
)

// countGroupedBy is a small helper for the "distribution across every row
// matching the current filter" breakdown charts (severity, status,
// priority, phase) -- the where/args pair came from
// alertFilterClause/incidentFilterClause, which already parameterize the
// one truly user-supplied part.
func countGroupedBy(ctx context.Context, tx pgx.Tx, table, column, where string, args []any) (map[string]int, error) {
	if !countGroupedByAllowedTables[table] || !countGroupedByAllowedColumns[column] {
		return nil, fmt.Errorf("countGroupedBy: table %q / column %q is not in the allowlist", table, column)
	}
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
// (empty = unrestricted), same semantics as StatsFilter.AllowedTags.
// since/until restrict to events that happened within [since, until] (the
// Dashboard's time-range filter, either end optional) -- filtered on each
// branch's own event timestamp (alert_events.created_at,
// alert_comments.created_at, ...), not the parent alert/incident's
// received_at/opened_at, since this is "what happened recently," not "which
// alerts/incidents are recent." Each UNION leg is a plain SELECT, so a
// WHERE clause can't scope individual legs -- the branches are built
// conditionally in Go instead, and branchFilter appends whichever of the
// tags/since/until conditions apply to each one.
func (r *DashboardRepository) RecentActivity(ctx context.Context, tx pgx.Tx, limit int, kind string, allowedTags []string, since, until *time.Time) ([]domain.ActivityEvent, error) {
	var branches []string
	var args []any
	tagsIdx := 0
	if len(allowedTags) > 0 {
		args = append(args, allowedTags)
		tagsIdx = len(args)
	}
	sinceIdx := 0
	if since != nil {
		args = append(args, *since)
		sinceIdx = len(args)
	}
	untilIdx := 0
	if until != nil {
		args = append(args, *until)
		untilIdx = len(args)
	}
	branchFilter := func(tagsAlias, eventAlias string) string {
		var conds []string
		if tagsIdx != 0 {
			conds = append(conds, fmt.Sprintf("%s.tags && $%d", tagsAlias, tagsIdx))
		}
		if sinceIdx != 0 {
			conds = append(conds, fmt.Sprintf("%s.created_at >= $%d", eventAlias, sinceIdx))
		}
		if untilIdx != 0 {
			conds = append(conds, fmt.Sprintf("%s.created_at <= $%d", eventAlias, untilIdx))
		}
		if len(conds) == 0 {
			return ""
		}
		return " where " + strings.Join(conds, " and ")
	}

	if kind == "" || kind == "alert" {
		branches = append(branches, `
			select 'alert' as kind, a.id, a.title, ae.event_type, ae.actor_type, ae.actor_id, ae.data, ae.created_at
			from alert_events ae join alerts a on a.id = ae.alert_id`+branchFilter("a", "ae"))
		branches = append(branches, `
			select 'alert' as kind, a.id, a.title, 'comment_added', 'user',
			       c.author_id, jsonb_build_object('authorName', c.author_name)::jsonb, c.created_at
			from alert_comments c join alerts a on a.id = c.alert_id`+branchFilter("a", "c"))
	}
	if kind == "" || kind == "incident" {
		branches = append(branches, `
			select 'incident' as kind, i.id, i.title, ie.event_type, ie.actor_type, ie.actor_id, ie.data, ie.created_at
			from incident_events ie join incidents i on i.id = ie.incident_id`+branchFilter("i", "ie"))
		branches = append(branches, `
			select 'incident' as kind, i.id, i.title, 'comment_added', 'user',
			       c.author_id, jsonb_build_object('authorName', c.author_name)::jsonb, c.created_at
			from incident_comments c join incidents i on i.id = c.incident_id`+branchFilter("i", "c"))
	}
	if len(branches) == 0 {
		return []domain.ActivityEvent{}, nil
	}

	args = append(args, limit)
	query := strings.Join(branches, " union all ") + fmt.Sprintf(" order by created_at desc limit $%d", len(args))
	return queryList(ctx, tx, query, func(row pgx.Row) (*domain.ActivityEvent, error) {
		var e domain.ActivityEvent
		if err := row.Scan(&e.Kind, &e.ContextID, &e.ContextTitle, &e.EventType, &e.ActorType, &e.ActorID, &e.Data, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan activity event: %w", err)
		}
		return &e, nil
	}, args...)
}
