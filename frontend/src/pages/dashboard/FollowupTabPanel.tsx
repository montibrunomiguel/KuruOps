import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import type { FollowupView } from "../../types/dashboard";
import { SeverityBadge, AlertStatusBadge } from "../../components/badges";
import { shortId } from "../../lib/format";
import { TimeRangeFilter, timeRangeParams, EMPTY_TIME_RANGE, type TimeRangeValue } from "../../components/TimeRangeFilter";

const HOUR_MS = 60 * 60 * 1000;

// Awaiting Triage / Waiting > 1h / Waiting > 4h are computed client-side
// from the follow-up alerts' receivedAt (per the approved plan) -- the
// backend endpoint returns the follow-up set itself (escalated/investigating
// alerts, see DashboardService.Followup), age-bucketing is just arithmetic
// on top, not worth a dedicated endpoint.
export function FollowupTabPanel() {
  const { t } = useTranslation();
  const { hasResourceAccess } = useAuth();
  const canOpenAlerts = hasResourceAccess("alerts");
  const [timeRange, setTimeRange] = useState<TimeRangeValue>(EMPTY_TIME_RANGE);
  // Memoized -- see AlertsTabPanel's identical comment (timeRangeParams()
  // calling Date.now() on every render would otherwise loop useList forever).
  const range = useMemo(() => timeRangeParams(timeRange), [timeRange]);

  const { data: viewData, loading, error, reload } = useList<FollowupView>(
    ["dashboard-followup", range.since, range.until],
    async (tk) => {
      const params = new URLSearchParams();
      if (range.since) params.set("since", range.since);
      if (range.until) params.set("until", range.until);
      return [await api.get<FollowupView>(`/api/v1/dashboard/followup?${params.toString()}`, tk)];
    },
  );
  const view = viewData?.[0];

  // Live updates -- see AlertsTabPanel's identical wiring for the reasoning.
  // Both event types can move an alert in/out of the follow-up set (an
  // alert's own status change, or an incident linking/unlinking it).
  useEventStream((event) => {
    if (event.type === "alert" || event.type === "incident") reload();
  });

  const queue = useMemo(() => {
    const alerts = view?.alerts ?? [];
    return [...alerts].sort((a, b) => new Date(a.receivedAt).getTime() - new Date(b.receivedAt).getTime());
  }, [view]);

  const now = Date.now();
  const waiting1h = queue.filter((a) => now - new Date(a.receivedAt).getTime() > HOUR_MS).length;
  const waiting4h = queue.filter((a) => now - new Date(a.receivedAt).getTime() > 4 * HOUR_MS).length;

  if (error) return <div className="error-banner">{error}</div>;

  return (
    <div>
      <div className="filter-bar">
        <TimeRangeFilter value={timeRange} onChange={setTimeRange} />
      </div>

      <div className="stat-grid" data-cols="3">
        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.followupTab.awaitingTriage")}</span>
          </div>
          <div className="stat-value">{loading ? "—" : queue.length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.followupTab.waiting1h")}</span>
          </div>
          <div className="stat-value">{loading ? "—" : waiting1h}</div>
        </div>
        <div className="stat-card">
          <div className="stat-card-head">
            <span className="stat-card-label">{t("dashboard.followupTab.waiting4h")}</span>
          </div>
          <div className="stat-value">{loading ? "—" : waiting4h}</div>
        </div>
      </div>

      <div className="panel">
        <div className="chart-card-title-row">
          <h2 className="panel-title">{t("dashboard.followupTab.triageQueueTitle")}</h2>
          <span className="chart-card-sub">{t("dashboard.followupTab.oldestFirst")}</span>
        </div>
        {!loading && queue.length === 0 && <div className="empty-state">{t("dashboard.followupTab.empty")}</div>}
        {queue.length > 0 && (
          <div className="table-wrap">
            <table className="table">
              <tbody>
                {queue.map((a, idx) => (
                  <tr key={a.id}>
                    <td className="rank-cell">#{idx + 1}</td>
                    <td>
                      <SeverityBadge severity={a.severity} />
                    </td>
                    <td className="mono">
                      {canOpenAlerts ? (
                        <Link to={`/alerts/${a.id}`} className="row-link-stretch" aria-label={a.title}>
                          {shortId(a.id)}
                        </Link>
                      ) : (
                        shortId(a.id)
                      )}
                    </td>
                    <td className="table-title-cell">{a.title}</td>
                    <td className="table-sub-cell">{a.source}</td>
                    <td>
                      <AlertStatusBadge status={a.status} />
                    </td>
                    <td className="age-text tone-critical">{formatAge(a.receivedAt)}</td>
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

function formatAge(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  const totalHours = Math.floor(ms / HOUR_MS);
  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  return `${days}d ${hours}h`;
}
