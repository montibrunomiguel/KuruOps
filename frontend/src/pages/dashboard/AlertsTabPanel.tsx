import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../api/client";
import { useList } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import { DashboardSkeleton } from "./DashboardSkeleton";
import type { ActivityEvent, DashboardStats } from "../../types/dashboard";
import type { AlertStatus, Severity } from "../../types/alerts";
import { TrendChart } from "../../components/charts/TrendChart";
import { DonutChart } from "../../components/charts/DonutChart";
import { BellIcon, FlagIcon, ClockIcon } from "../../components/icons";
import { SEVERITY_ORDER, SEVERITY_COLOR, ALERT_STATUS_ORDER, ALERT_STATUS_COLOR } from "../../lib/chartColors";
import { describeActivity } from "../../lib/activityText";
import { formatDuration, formatRelative } from "../../lib/format";
import { TimeRangeFilter, timeRangeParams, EMPTY_TIME_RANGE, type TimeRangeValue } from "../../components/TimeRangeFilter";
import { MultiSelectFilter } from "../../components/MultiSelectFilter";
import { TagPicker } from "../../components/TagPicker";
import { AssigneePicker } from "../../components/AssigneePicker";

export function AlertsTabPanel() {
  const { t } = useTranslation();
  const [severity, setSeverity] = useState<Severity[]>([]);
  const [status, setStatus] = useState<AlertStatus[]>([]);
  const [source, setSource] = useState("");
  const [tag, setTag] = useState<string[]>([]);
  const [analystIds, setAnalystIds] = useState<string[]>([]);
  const [timeRange, setTimeRange] = useState<TimeRangeValue>(EMPTY_TIME_RANGE);
  // Memoized -- timeRangeParams() calls Date.now() for preset ranges, so
  // recomputing it on every render would produce a new object each time even
  // when timeRange hasn't changed, which would loop useList's effect (since
  // range is one of its deps) forever.
  const range = useMemo(() => timeRangeParams(timeRange), [timeRange]);

  const { data: statsData, loading: statsLoading, error: statsError, reload: reloadStats } = useList<DashboardStats>(
    ["dashboard-alerts-stats", severity.join(","), status.join(","), source, tag.join(","), analystIds.join(","), range.since, range.until],
    async (tk) => {
      const params = new URLSearchParams();
      if (severity.length > 0) params.set("alertSeverity", severity.join(","));
      if (status.length > 0) params.set("alertStatus", status.join(","));
      if (source) params.set("alertSource", source);
      if (tag.length > 0) params.set("alertTag", tag.join(","));
      if (analystIds.length > 0) params.set("assignedAnalystId", analystIds.join(","));
      if (range.since) params.set("since", range.since);
      if (range.until) params.set("until", range.until);
      return [await api.get<DashboardStats>(`/api/v1/dashboard/stats?${params.toString()}`, tk)];
    },
  );
  const stats = statsData?.[0];

  const { data: activityData, loading: activityLoading, reload: reloadActivity } = useList<ActivityEvent>(
    ["dashboard-alerts-activity", range.since, range.until],
    (tk) => {
      const params = new URLSearchParams({ kind: "alert", limit: "6" });
      if (range.since) params.set("since", range.since);
      if (range.until) params.set("until", range.until);
      return api.get<ActivityEvent[]>(`/api/v1/dashboard/activity?${params.toString()}`, tk);
    },
  );

  // Live updates: an alert changing anywhere (this tab, another analyst,
  // cmd/api's own interactive handlers -- see cmd/api/main.go's
  // EnableEventPublishing wiring) refreshes both the KPI cards and the
  // activity feed.
  useEventStream((event) => {
    if (event.type !== "alert") return;
    reloadStats();
    reloadActivity();
  });

  // hasLoadedOnce gates DashboardSkeleton to the very first paint only --
  // statsLoading itself flips true again on every filter change, and this
  // tab's stat cards/charts already have their own inline "—" placeholders
  // for that case (see statsLoading usage below).
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  useEffect(() => {
    if (!statsLoading) setHasLoadedOnce(true);
  }, [statsLoading]);

  if (statsLoading && !hasLoadedOnce) return <DashboardSkeleton />;
  if (statsError) return <div className="error-banner">{statsError}</div>;

  const bySeverity = stats?.alertsBySeverity ?? {};
  const maxSeverity = Math.max(1, ...SEVERITY_ORDER.map((s) => bySeverity[s] ?? 0));
  const byStatus = stats?.alertStatusDistribution ?? {};
  const acknowledgedApprox = Object.entries(byStatus).reduce((sum, [k, v]) => (k === "open" ? sum : sum + v), 0);

  return (
    <div>
      <div className="filter-bar">
        <MultiSelectFilter
          ariaLabel={t("dashboard.filters.severityFilterLabel")}
          placeholder={t("dashboard.filters.allSeverities")}
          value={severity}
          onChange={(next) => setSeverity(next as Severity[])}
          options={[
            { value: "critical", label: t("common.severity.critical") },
            { value: "high", label: t("common.severity.high") },
            { value: "medium", label: t("common.severity.medium") },
            { value: "low", label: t("common.severity.low") },
            { value: "informational", label: t("common.severity.informational") },
          ]}
        />
        <MultiSelectFilter
          ariaLabel={t("dashboard.filters.statusFilterLabel")}
          placeholder={t("dashboard.filters.allStatuses")}
          value={status}
          onChange={(next) => setStatus(next as AlertStatus[])}
          options={[
            { value: "open", label: t("common.alertStatus.open") },
            { value: "investigating", label: t("common.alertStatus.investigating") },
            { value: "escalated", label: t("common.alertStatus.escalated") },
            { value: "closed", label: t("common.alertStatus.closed") },
          ]}
        />
        <input
          className="input"
          aria-label={t("dashboard.filters.sourceFilterLabel")}
          placeholder={t("dashboard.filters.allSources")}
          value={source}
          onChange={(e) => setSource(e.target.value)}
        />
        <TagPicker value={tag} onChange={setTag} />
        <AssigneePicker value={analystIds} onChange={setAnalystIds} />
        <TimeRangeFilter value={timeRange} onChange={setTimeRange} />
      </div>

      <div className="stat-grid" data-cols="4">
        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.alertsTab.openAlerts")}</span>
            <span className="stat-icon-box tone-accent">
              <BellIcon width={15} height={15} />
            </span>
          </div>
          <div className="stat-value">{statsLoading ? "—" : stats?.openAlerts ?? 0}</div>
        </div>

        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.alertsTab.criticalSeverity")}</span>
            <span className="stat-icon-box tone-high">
              <FlagIcon width={15} height={15} />
            </span>
          </div>
          <div className="stat-value">{statsLoading ? "—" : stats?.criticalAlerts ?? 0}</div>
          <div className="stat-sub">{t("dashboard.alertsTab.criticalSeveritySub")}</div>
        </div>

        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.alertsTab.highSeverity")}</span>
            <span className="stat-icon-box tone-muted">
              <FlagIcon width={15} height={15} />
            </span>
          </div>
          <div className="stat-value">{statsLoading ? "—" : stats?.highAlerts ?? 0}</div>
          <div className="stat-sub">{t("dashboard.alertsTab.highSeveritySub")}</div>
        </div>

        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.alertsTab.avgMttaMttr")}</span>
            <span className="stat-icon-box tone-success">
              <ClockIcon width={15} height={15} />
            </span>
          </div>
          <div className="stat-value">
            {statsLoading ? "—" : `${formatDuration(stats?.alertAvgMttaSeconds)} / ${formatDuration(stats?.alertAvgMttrSeconds)}`}
          </div>
          <div className="stat-sub">
            {t("dashboard.alertsTab.avgMttaMttrSub", { acked: acknowledgedApprox, closed: byStatus.closed ?? 0 })}
          </div>
        </div>
      </div>

      <div className="dashboard-grid-2">
        <div className="panel">
          <div className="chart-card-title-row">
            <h2 className="panel-title">{t("dashboard.alertsTab.trendTitle")}</h2>
            <span className="chart-card-sub">{t("dashboard.alertsTab.last14Days")}</span>
          </div>
          <TrendChart points={stats?.alertTrend ?? []} />
          <div className="chart-legend">
            <span className="chart-legend-item">
              <span className="chart-legend-swatch" style={{ background: "var(--accent)" }} />
              {t("dashboard.alertsTab.legendReceived")}
            </span>
            <span className="chart-legend-item">
              <span className="chart-legend-swatch dot" style={{ background: "var(--success)" }} />
              {t("dashboard.alertsTab.legendMttr")}
            </span>
          </div>
        </div>

        <div className="panel">
          <h2 className="panel-title" style={{ marginBottom: 14 }}>
            {t("dashboard.alertsTab.bySeverityTitle")}
          </h2>
          <div className="breakdown-list">
            {SEVERITY_ORDER.map((sev) => {
              const count = bySeverity[sev] ?? 0;
              return (
                <div key={sev}>
                  <div className="breakdown-row-head">
                    <span className="breakdown-row-head-label">
                      <span className="legend-dot" style={{ background: SEVERITY_COLOR[sev] }} />
                      {t(`common.severity.${sev}`)}
                    </span>
                    <span>{count}</span>
                  </div>
                  <div className="breakdown-bar-track">
                    <div
                      className="breakdown-bar-fill"
                      style={{ width: `${(count / maxSeverity) * 100}%`, background: SEVERITY_COLOR[sev] }}
                    />
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </div>

      <div className="dashboard-grid-2">
        <div className="panel">
          <h2 className="panel-title" style={{ marginBottom: 14 }}>
            {t("dashboard.alertsTab.byAnalystTitle")}
          </h2>
          <div className="breakdown-list">
            {(stats?.alertsByAnalyst ?? []).map((c) => {
              const max = Math.max(1, ...(stats?.alertsByAnalyst ?? []).map((x) => x.count));
              return (
                <div key={c.id ?? "unassigned"}>
                  <div className="breakdown-row-head">
                    <span className="breakdown-row-head-label">{c.id ? c.name : t("dashboard.alertsTab.unassigned")}</span>
                    <span>{c.count}</span>
                  </div>
                  <div className="breakdown-bar-track">
                    <div className="breakdown-bar-fill" style={{ width: `${(c.count / max) * 100}%`, background: "var(--accent)" }} />
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        <div className="panel">
          <h2 className="panel-title" style={{ marginBottom: 16 }}>
            {t("dashboard.alertsTab.statusDistributionTitle")}
          </h2>
          <div className="donut-layout">
            <DonutChart
              slices={ALERT_STATUS_ORDER.filter((s) => (byStatus[s] ?? 0) > 0).map((s) => ({
                key: s,
                label: t(`common.alertStatus.${s}`),
                value: byStatus[s] ?? 0,
                color: ALERT_STATUS_COLOR[s],
              }))}
            />
            <div className="legend-list">
              {ALERT_STATUS_ORDER.map((s) => (
                <div className="legend-row" key={s}>
                  <span className="legend-row-label">
                    <span className="legend-dot" style={{ background: ALERT_STATUS_COLOR[s] }} />
                    {t(`common.alertStatus.${s}`)}
                  </span>
                  <span className="legend-row-value">{byStatus[s] ?? 0}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>

      <div className="panel">
        <h2 className="panel-title" style={{ marginBottom: 14 }}>
          {t("dashboard.alertsTab.recentActivityTitle")}
        </h2>
        {!activityLoading && (Array.isArray(activityData) ? activityData : []).length === 0 && (
          <div className="empty-state">{t("dashboard.alertsTab.noActivity")}</div>
        )}
        <div className="activity-list">
          {(Array.isArray(activityData) ? activityData : []).map((ev, idx) => {
            const { icon, tone, text } = describeActivity(ev, t);
            return (
              <div className="activity-item" key={`${ev.kind}-${ev.contextId}-${idx}`}>
                <span className={`activity-icon-box ${tone}`}>{icon}</span>
                <div>
                  <p className="activity-text">{text}</p>
                  <span className="activity-time">{formatRelative(ev.createdAt)}</span>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
