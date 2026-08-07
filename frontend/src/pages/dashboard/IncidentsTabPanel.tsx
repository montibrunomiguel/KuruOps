import { useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api } from "../../api/client";
import { useList } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import type { DashboardStats } from "../../types/dashboard";
import type { Incident } from "../../types/incidents";
import type { Severity } from "../../types/alerts";
import { SeverityBadge, PhasePill } from "../../components/badges";
import { ShieldIcon, ClockIcon, FlagIcon } from "../../components/icons";
import { PRIORITY_ORDER, PHASE_ORDER, PHASE_COLOR } from "../../lib/chartColors";
import { formatDuration, shortId } from "../../lib/format";

export function IncidentsTabPanel() {
  const { t } = useTranslation();
  const [severity, setSeverity] = useState<Severity | "">("");
  const [tag, setTag] = useState("");

  const { data: statsData, loading: statsLoading, error: statsError, reload: reloadStats } = useList<DashboardStats>(
    async (tk) => {
      const params = new URLSearchParams();
      if (severity) params.set("incidentSeverity", severity);
      if (tag) params.set("incidentTag", tag);
      return [await api.get<DashboardStats>(`/api/v1/dashboard/stats?${params.toString()}`, tk)];
    },
    [severity, tag],
  );
  const stats = statsData?.[0];

  const { data: incidents, loading: incidentsLoading, reload: reloadIncidents } = useList<Incident>(
    (tk) => {
      const params = new URLSearchParams();
      if (severity) params.set("severity", severity);
      if (tag) params.set("tag", tag);
      params.set("limit", "5");
      return api.get<Incident[]>(`/api/v1/incidents?${params.toString()}`, tk);
    },
    [severity, tag],
  );

  // Live updates -- see AlertsTabPanel's identical wiring for the reasoning.
  useEventStream((event) => {
    if (event.type !== "incident") return;
    reloadStats();
    reloadIncidents();
  });

  if (statsError) return <div className="error-banner">{statsError}</div>;

  const byPriority = stats?.incidentsByPriority ?? {};
  const maxPriority = Math.max(1, ...PRIORITY_ORDER.map((p) => byPriority[p] ?? 0));
  const byPhase = stats?.incidentsByPhase ?? {};

  return (
    <div>
      <div className="filter-bar">
        <select className="select" value={severity} onChange={(e) => setSeverity(e.target.value as Severity | "")}>
          <option value="">{t("dashboard.filters.allSeverities")}</option>
          <option value="critical">{t("common.severity.critical")}</option>
          <option value="high">{t("common.severity.high")}</option>
          <option value="medium">{t("common.severity.medium")}</option>
          <option value="low">{t("common.severity.low")}</option>
          <option value="informational">{t("common.severity.informational")}</option>
        </select>
        <input className="input" placeholder={t("dashboard.filters.allTags")} value={tag} onChange={(e) => setTag(e.target.value)} />
      </div>

      <div className="stat-grid">
        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.incidentsTab.activeIncidents")}</span>
            <span className="stat-icon-box tone-accent">
              <ShieldIcon width={15} height={15} />
            </span>
          </div>
          <div className="stat-value">{statsLoading ? "—" : stats?.activeIncidents ?? 0}</div>
          <div className="stat-sub">
            {t("dashboard.incidentsTab.activeIncidentsSub", { count: byPhase.post_incident ?? 0 })}
          </div>
        </div>

        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.incidentsTab.slaBreached")}</span>
            <span className={`stat-icon-box ${stats?.slaBreachedCount ? "tone-critical" : "tone-muted"}`}>
              <ClockIcon width={15} height={15} />
            </span>
          </div>
          <div className="stat-value">{statsLoading ? "—" : stats?.slaBreachedCount ?? 0}</div>
          <div className={`stat-sub ${stats?.slaBreachedCount ? "tone-critical" : ""}`}>
            {stats?.slaBreachedCount ? t("dashboard.incidentsTab.slaBreachedSub") : t("dashboard.incidentsTab.slaBreachedSubOk")}
          </div>
        </div>

        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.incidentsTab.p1Open")}</span>
            <span className="stat-icon-box tone-high">
              <FlagIcon width={15} height={15} />
            </span>
          </div>
          <div className="stat-value">{statsLoading ? "—" : stats?.p1OpenCount ?? 0}</div>
          <div className="stat-sub">{t("dashboard.incidentsTab.p1OpenSub")}</div>
        </div>

        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.incidentsTab.avgMttaMttr")}</span>
            <span className="stat-icon-box tone-success">
              <ClockIcon width={15} height={15} />
            </span>
          </div>
          <div className="stat-value">
            {statsLoading ? "—" : `${formatDuration(stats?.incidentAvgMttaSeconds)} / ${formatDuration(stats?.incidentAvgMttrSeconds)}`}
          </div>
          <div className="stat-sub">{t("dashboard.incidentsTab.avgMttaMttrSub")}</div>
        </div>
      </div>

      <div className="dashboard-grid-2">
        <div className="panel">
          <h2 className="panel-title" style={{ marginBottom: 16 }}>
            {t("dashboard.incidentsTab.byPriorityTitle")}
          </h2>
          <div className="breakdown-list">
            {PRIORITY_ORDER.map((p) => {
              const count = byPriority[p] ?? 0;
              return (
                <div key={p}>
                  <div className="breakdown-row-head">
                    <span className="breakdown-row-head-label">{p.toUpperCase()}</span>
                    <span>{count}</span>
                  </div>
                  <div className="breakdown-bar-track">
                    <div className="breakdown-bar-fill" style={{ width: `${(count / maxPriority) * 100}%`, background: "var(--accent)" }} />
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        <div className="panel">
          <h2 className="panel-title" style={{ marginBottom: 14 }}>
            {t("dashboard.incidentsTab.byPhaseTitle")}
          </h2>
          <div className="legend-list">
            {PHASE_ORDER.map((phase) => (
              <div className="legend-row" key={phase}>
                <span className="legend-row-label">
                  <span className="legend-dot" style={{ background: PHASE_COLOR[phase] }} />
                  {t(`common.phase.${phase}`)}
                </span>
                <span className="legend-row-value">{byPhase[phase] ?? 0}</span>
              </div>
            ))}
          </div>
        </div>
      </div>

      <div className="panel">
        <h2 className="panel-title" style={{ marginBottom: 10 }}>
          {t("dashboard.incidentsTab.recentIncidentsTitle")}
        </h2>
        {!incidentsLoading && (incidents ?? []).length === 0 && (
          <div className="empty-state">{t("dashboard.incidentsTab.noIncidents")}</div>
        )}
        {(incidents ?? []).length > 0 && (
          <div className="table-wrap">
            <table className="table">
              <tbody>
                {(incidents ?? []).map((i) => (
                  <tr key={i.id}>
                    <td>
                      <SeverityBadge severity={i.severity} />
                    </td>
                    <td className="mono">
                      <Link to={`/incidents/${i.id}`} className="row-link-stretch" aria-label={i.title}>
                        {shortId(i.id)}
                      </Link>
                    </td>
                    <td className="table-title-cell">{i.title}</td>
                    <td>
                      <PhasePill phase={i.phase} />
                    </td>
                    <td className="table-sub-cell">
                      {i.assignees.length > 0 ? i.assignees.map((a) => a.name).join(", ") : "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
