import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { usePagedList, mutationErrorMessage } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import { useDebouncedValue } from "../../hooks/useDebouncedValue";
import type { Alert, AlertStatus, Severity } from "../../types/alerts";
import type { BulkResponse } from "../../types/api";
import { SeverityBadge, AlertStatusBadge } from "../../components/badges";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { SeverityFilter } from "../../components/SeverityFilter";
import { TimeRangeFilter, timeRangeParams, EMPTY_TIME_RANGE, type TimeRangeValue } from "../../components/TimeRangeFilter";
import { Pagination } from "../../components/Pagination";
import { formatRelative, shortId } from "../../lib/format";

// Bulk status-change deliberately excludes "closed" -- ChangeStatus (and so
// BulkChangeStatus, which just loops over it) rejects a direct transition
// to closed so classification is always captured; closing an alert still
// requires the existing per-item Close & Classify flow on the detail page.
const BULK_STATUS_OPTIONS: AlertStatus[] = ["open", "investigating", "escalated"];

export function AlertsListPage() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [severity, setSeverity] = useState<Severity | "">("");
  const [status, setStatus] = useState<AlertStatus | "">("");
  const [source, setSource] = useState("");
  const [correlated, setCorrelated] = useState<"" | "true" | "false">("");
  const [tag, setTag] = useState("");
  const [q, setQ] = useState("");
  const debouncedQ = useDebouncedValue(q);
  const [timeRange, setTimeRange] = useState<TimeRangeValue>(EMPTY_TIME_RANGE);
  // Memoized so timeRangeParams' internal Date.now() (for preset ranges)
  // isn't recomputed on every render -- only when the picker's own value
  // actually changes, same reasoning as the Dashboard tab panels.
  const range = useMemo(() => timeRangeParams(timeRange), [timeRange]);

  const {
    items: alerts,
    total,
    page,
    pageSize,
    totalPages,
    loading,
    error,
    setPage,
    setPageSize,
    reload,
  } = usePagedList<Alert>(
    ["alerts", severity, status, source, correlated, tag, debouncedQ, range.since, range.until],
    (tk, limit, offset) => {
      const params = new URLSearchParams();
      if (severity) params.set("severity", severity);
      if (status) params.set("status", status);
      if (source) params.set("source", source);
      if (correlated) params.set("correlated", correlated);
      if (tag) params.set("tag", tag);
      if (debouncedQ) params.set("q", debouncedQ);
      if (range.since) params.set("since", range.since);
      if (range.until) params.set("until", range.until);
      params.set("limit", String(limit));
      params.set("offset", String(offset));
      return api.getPaged<Alert>(`/api/v1/alerts?${params.toString()}`, tk);
    },
  );

  // Row selection -- scoped to what's currently on screen, not persisted
  // across a page/filter change, so the bulk toolbar's count never refers
  // to rows the analyst can no longer see. Anything that swaps out which
  // alerts are shown resets it via the effect below.
  const [selected, setSelected] = useState<Set<string>>(new Set());
  useEffect(() => {
    setSelected(new Set());
  }, [severity, status, source, correlated, tag, debouncedQ, range.since, range.until, page, pageSize]);

  function toggleRow(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  const allOnPageSelected = alerts.length > 0 && alerts.every((a) => selected.has(a.id));
  const someOnPageSelected = alerts.some((a) => selected.has(a.id));
  const selectAllRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (selectAllRef.current) {
      selectAllRef.current.indeterminate = someOnPageSelected && !allOnPageSelected;
    }
  }, [someOnPageSelected, allOnPageSelected]);

  function toggleSelectAll() {
    if (allOnPageSelected) {
      setSelected(new Set());
    } else {
      setSelected(new Set(alerts.map((a) => a.id)));
    }
  }

  const [bulkStatus, setBulkStatus] = useState<AlertStatus>("investigating");
  const [applying, setApplying] = useState(false);
  const [bulkError, setBulkError] = useState<string | null>(null);
  const [bulkSummary, setBulkSummary] = useState<{ success: number; failed: number } | null>(null);

  async function applyBulkStatus() {
    setApplying(true);
    setBulkError(null);
    setBulkSummary(null);
    try {
      const resp = await api.post<BulkResponse>(
        "/api/v1/alerts/bulk/status",
        { ids: Array.from(selected), status: bulkStatus },
        token,
      );
      const success = resp.results.filter((r) => r.success).length;
      const failed = resp.results.length - success;
      setBulkSummary({ success, failed });
      setSelected(new Set());
      reload();
    } catch (err) {
      setBulkError(mutationErrorMessage(err));
    } finally {
      setApplying(false);
    }
  }

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
          <input
            className="input input-search"
            placeholder={t("alerts.searchPlaceholder")}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
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
        {!loading && <span className="chart-card-sub">{t("alerts.count", { count: total })}</span>}
      </div>

      {selected.size > 0 && (
        <div className="bulk-toolbar">
          <span className="bulk-toolbar-count">{t("alerts.bulk.selected", { count: selected.size })}</span>
          <select
            className="select"
            aria-label={t("alerts.bulk.statusLabel")}
            value={bulkStatus}
            onChange={(e) => setBulkStatus(e.target.value as AlertStatus)}
            disabled={applying}
          >
            {BULK_STATUS_OPTIONS.map((s) => (
              <option key={s} value={s}>
                {t(`common.alertStatus.${s}`)}
              </option>
            ))}
          </select>
          <button className="btn btn-primary btn-sm" onClick={() => void applyBulkStatus()} disabled={applying}>
            {applying ? t("alerts.bulk.applying") : t("alerts.bulk.apply")}
          </button>
          <button className="btn btn-ghost btn-sm" onClick={() => setSelected(new Set())} disabled={applying}>
            {t("alerts.bulk.clearSelection")}
          </button>
        </div>
      )}
      {bulkError && <div className="error-banner">{bulkError}</div>}
      {bulkSummary && (
        <div className="helper-text" style={{ marginBottom: 12 }}>
          {bulkSummary.failed > 0
            ? t("alerts.bulk.resultSummary", { success: bulkSummary.success, failed: bulkSummary.failed })
            : t("alerts.bulk.allSucceeded", { count: bulkSummary.success })}
        </div>
      )}

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
                    <th className="table-select-cell">
                      <input
                        type="checkbox"
                        ref={selectAllRef}
                        checked={allOnPageSelected}
                        onChange={toggleSelectAll}
                        aria-label={t("alerts.bulk.selectAllAria")}
                      />
                    </th>
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
                      <td className="table-select-cell">
                        <input
                          type="checkbox"
                          checked={selected.has(a.id)}
                          onChange={() => toggleRow(a.id)}
                          aria-label={t("alerts.bulk.selectRowAria", { id: a.id.slice(0, 8) })}
                        />
                      </td>
                      <td className="mono">
                        <Link to={`/alerts/${a.id}`} className="row-link-stretch" aria-label={a.title}>
                          {a.id.slice(0, 8)}
                        </Link>
                      </td>
                      <td className="table-title-cell">
                        {a.title}
                        {a.duplicateCount > 0 && (
                          <span
                            className="badge badge-muted"
                            style={{ marginLeft: 6 }}
                            title={t("alerts.duplicateBadgeTitle", { count: a.duplicateCount })}
                          >
                            +{a.duplicateCount}
                          </span>
                        )}
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
            <Pagination
              page={page}
              pageSize={pageSize}
              total={total}
              totalPages={totalPages}
              onPageChange={setPage}
              onPageSizeChange={setPageSize}
            />
          </>
        )}
      </div>
    </div>
  );
}
