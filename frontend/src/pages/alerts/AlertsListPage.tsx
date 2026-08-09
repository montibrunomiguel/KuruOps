import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api } from "../../api/client";
import { usePaginatedList } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import type { Alert, AlertStatus, Severity } from "../../types/alerts";
import { SeverityBadge, AlertStatusBadge } from "../../components/badges";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { SeverityFilter } from "../../components/SeverityFilter";
import { TimeRangeFilter, timeRangeParams, EMPTY_TIME_RANGE, type TimeRangeValue } from "../../components/TimeRangeFilter";
import { formatRelative, shortId } from "../../lib/format";

export function AlertsListPage() {
  const { t } = useTranslation();
  const [severity, setSeverity] = useState<Severity | "">("");
  const [status, setStatus] = useState<AlertStatus | "">("");
  const [source, setSource] = useState("");
  const [correlated, setCorrelated] = useState<"" | "true" | "false">("");
  const [tag, setTag] = useState("");
  const [timeRange, setTimeRange] = useState<TimeRangeValue>(EMPTY_TIME_RANGE);
  // Memoized so timeRangeParams' internal Date.now() (for preset ranges)
  // isn't recomputed on every render -- only when the picker's own value
  // actually changes, same reasoning as the Dashboard tab panels.
  const range = useMemo(() => timeRangeParams(timeRange), [timeRange]);

  const {
    items: alerts,
    loading,
    loadingMore,
    error,
    hasMore,
    loadMore,
    reload,
  } = usePaginatedList<Alert>(
    (tk, limit, offset) => {
      const params = new URLSearchParams();
      if (severity) params.set("severity", severity);
      if (status) params.set("status", status);
      if (source) params.set("source", source);
      if (correlated) params.set("correlated", correlated);
      if (tag) params.set("tag", tag);
      if (range.since) params.set("since", range.since);
      if (range.until) params.set("until", range.until);
      params.set("limit", String(limit));
      params.set("offset", String(offset));
      return api.get<Alert[]>(`/api/v1/alerts?${params.toString()}`, tk);
    },
    [severity, status, source, correlated, tag, range.since, range.until],
  );

  // Live updates: another analyst (or the same one, in another tab)
  // changing an alert re-fetches the first page from scratch -- simplest
  // correct behavior, since a live diff against an offset-paginated list
  // would need much more bookkeeping for a rare-ish event.
  useEventStream((event) => {
    if (event.type === "alert") reload();
  });

  return (
    <div>
      <div className="toolbar">
        <div className="toolbar-title">
          <h1 className="page-title">{t("alerts.title")}</h1>
          <p className="page-sub" style={{ marginBottom: 0 }}>
            {t("alerts.subtitle")}
          </p>
        </div>
        <WebhookStatusIndicator />
      </div>

      <div className="filter-bar" style={{ justifyContent: "space-between" }}>
        <div style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
          <SeverityFilter value={severity} onChange={setSeverity} />
          <select
            className="select"
            aria-label={t("dashboard.filters.statusFilterLabel")}
            value={status}
            onChange={(e) => setStatus(e.target.value as AlertStatus | "")}
          >
            <option value="">{t("dashboard.filters.allStatuses")}</option>
            <option value="open">{t("common.alertStatus.open")}</option>
            <option value="investigating">{t("common.alertStatus.investigating")}</option>
            <option value="escalated">{t("common.alertStatus.escalated")}</option>
            <option value="closed">{t("common.alertStatus.closed")}</option>
          </select>
          <input
            className="input"
            aria-label={t("dashboard.filters.sourceFilterLabel")}
            placeholder={t("dashboard.filters.allSources")}
            value={source}
            onChange={(e) => setSource(e.target.value)}
          />
          <select
            className="select"
            aria-label={t("dashboard.filters.correlatedFilterLabel")}
            value={correlated}
            onChange={(e) => setCorrelated(e.target.value as "" | "true" | "false")}
          >
            <option value="">{t("dashboard.filters.correlatedAny")}</option>
            <option value="true">{t("dashboard.filters.correlatedYes")}</option>
            <option value="false">{t("dashboard.filters.correlatedNo")}</option>
          </select>
          <input
            className="input"
            aria-label={t("dashboard.filters.tagsFilterLabel")}
            placeholder={t("dashboard.filters.allTags")}
            value={tag}
            onChange={(e) => setTag(e.target.value)}
          />
          <TimeRangeFilter value={timeRange} onChange={setTimeRange} />
        </div>
        {!loading && <span className="chart-card-sub">{t("alerts.count", { count: alerts.length })}</span>}
      </div>

      <div className="panel">
        {error && <div className="error-banner">{error}</div>}
        {loading && <div className="empty-state">{t("common.loading")}</div>}
        {!loading && alerts.length === 0 && <div className="empty-state">{t("alerts.empty")}</div>}
        {!loading && alerts.length > 0 && (
          <>
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th>{t("alerts.table.id")}</th>
                    <th>{t("alerts.table.title")}</th>
                    <th>{t("alerts.table.source")}</th>
                    <th>{t("alerts.table.severity")}</th>
                    <th>{t("alerts.table.status")}</th>
                    <th>{t("alerts.table.received")}</th>
                    <th>{t("alerts.table.correlated")}</th>
                    <th>{t("alerts.table.assignee")}</th>
                  </tr>
                </thead>
                <tbody>
                  {alerts.map((a) => (
                    <tr key={a.id}>
                      <td className="mono">
                        <Link to={`/alerts/${a.id}`} className="row-link-stretch" aria-label={a.title}>
                          {a.id.slice(0, 8)}
                        </Link>
                      </td>
                      <td className="table-title-cell">
                        {a.title}
                        {a.tags.length > 0 && (
                          <div className="tag-chip-list" style={{ marginTop: 4 }}>
                            {a.tags.map((tagName) => (
                              <span className="tag-chip" key={tagName}>
                                {tagName}
                              </span>
                            ))}
                          </div>
                        )}
                      </td>
                      <td>{a.source}</td>
                      <td>
                        <SeverityBadge severity={a.severity} />
                      </td>
                      <td>
                        <AlertStatusBadge status={a.status} />
                      </td>
                      <td className="mono">{formatRelative(a.receivedAt)}</td>
                      <td>
                        {a.incidentId ? (
                          <span className="badge badge-success">{t("common.yes")}</span>
                        ) : (
                          <span className="badge badge-muted">{t("common.no")}</span>
                        )}
                      </td>
                      <td className="table-sub-cell">
                        {a.assignedAnalystName ?? (a.assignedAnalystId ? shortId(a.assignedAnalystId) : "—")}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {hasMore && (
              <div style={{ textAlign: "center", marginTop: 14 }}>
                <button className="btn btn-sm" onClick={loadMore} disabled={loadingMore}>
                  {loadingMore ? t("common.loading") : t("alerts.loadMore")}
                </button>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
