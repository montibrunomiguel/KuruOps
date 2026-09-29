import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { usePagedList } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import { useDebouncedValue } from "../../hooks/useDebouncedValue";
import { useRowSelection } from "../../hooks/useRowSelection";
import { useBulkAction } from "../../hooks/useBulkAction";
import type { Alert, AlertStatus, Severity, Classification } from "../../types/alerts";
import { SeverityBadge, AlertStatusBadge } from "../../components/badges";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { MultiSelectFilter } from "../../components/MultiSelectFilter";
import { TimeRangeFilter, timeRangeParams, EMPTY_TIME_RANGE, type TimeRangeValue } from "../../components/TimeRangeFilter";
import { Pagination } from "../../components/Pagination";
import { formatRelative, shortId } from "../../lib/format";
import { severityFilterOptions, alertStatusFilterOptions } from "../../lib/chartColors";

// Bulk status-change deliberately excludes "closed" -- ChangeStatus (and so
// BulkChangeStatus, which just loops over it) rejects a direct transition
// to closed so classification is always captured; closing an alert still
// requires the existing per-item Close & Classify flow on the detail page.
// "closed" belongs here even though it takes a different endpoint: from the
// analyst's side it is the same act -- pick what these alerts now are, apply.
// Choosing it reveals the classification picker, because closing without one
// is not allowed (see AlertService.Close).
const BULK_STATUS_OPTIONS: AlertStatus[] = ["open", "investigating", "escalated", "closed"];
const BULK_CLASSIFICATIONS: Classification[] = ["false_positive", "true_positive", "authorized_event"];

export function AlertsListPage() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [severity, setSeverity] = useState<Severity[]>([]);
  const [status, setStatus] = useState<AlertStatus[]>([]);
  const [source, setSource] = useState("");
  const [correlated, setCorrelated] = useState<"" | "true" | "false">("");
  const [tag, setTag] = useState("");
  const [q, setQ] = useState("");
  const debouncedQ = useDebouncedValue(q);
  // Trimmed so a whitespace-only search (e.g. an accidental space-bar
  // press) doesn't count as "changed" -- resetting the page/selection and
  // sending a query the backend would trim to nothing anyway -- the way it
  // would if debouncedQ were used raw here.
  const trimmedQ = debouncedQ.trim();
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
    ["alerts", severity.join(","), status.join(","), source, correlated, tag, trimmedQ, range.since, range.until],
    (tk, limit, offset) => {
      const params = new URLSearchParams();
      if (severity.length > 0) params.set("severity", severity.join(","));
      if (status.length > 0) params.set("status", status.join(","));
      if (source) params.set("source", source);
      if (correlated) params.set("correlated", correlated);
      if (tag) params.set("tag", tag);
      if (trimmedQ) params.set("q", trimmedQ);
      if (range.since) params.set("since", range.since);
      if (range.until) params.set("until", range.until);
      params.set("limit", String(limit));
      params.set("offset", String(offset));
      return api.getPaged<Alert>(`/api/v1/alerts?${params.toString()}`, tk);
    },
  );

  // Row selection -- scoped to what's currently on screen, not persisted
  // across a page/filter change, so the bulk toolbar's count never refers
  // to rows the analyst can no longer see. See useRowSelection's own doc
  // comment for why resetDeps is passed through this way.
  const { selected, toggleRow, toggleSelectAll, allOnPageSelected, selectAllRef, clear } = useRowSelection(
    alerts,
    (a) => a.id,
    [severity, status, source, correlated, tag, trimmedQ, range.since, range.until, page, pageSize],
  );

  const [bulkStatus, setBulkStatus] = useState<AlertStatus>("investigating");
  const [bulkClassification, setBulkClassification] = useState<Classification>("false_positive");
  const [bulkComment, setBulkComment] = useState("");
  const statusBulk = useBulkAction("/api/v1/alerts/bulk/status", token);
  // Closing has its own endpoint because it carries a classification, so it
  // gets its own hook instance. Only one of the two is ever in flight, and
  // the toolbar reads whichever matches the chosen status.
  const closeBulk = useBulkAction("/api/v1/alerts/bulk/close", token);
  const closing = bulkStatus === "closed";
  const active = closing ? closeBulk : statusBulk;
  const applying = active.applying;
  const bulkError = active.error;
  const bulkSummary = active.summary;

  function applyBulkStatus() {
    const ids = Array.from(selected);
    const done = () => {
      clear();
      setBulkComment("");
      reload();
    };
    if (closing) {
      void closeBulk.apply(ids, { classification: bulkClassification, comment: bulkComment }, done);
      return;
    }
    void statusBulk.apply(ids, { status: bulkStatus }, done);
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
          <MultiSelectFilter
            ariaLabel={t("dashboard.filters.severityFilterLabel")}
            placeholder={t("dashboard.filters.allSeverities")}
            value={severity}
            onChange={(next) => setSeverity(next as Severity[])}
            options={severityFilterOptions(t)}
          />
          <MultiSelectFilter
            ariaLabel={t("dashboard.filters.statusFilterLabel")}
            placeholder={t("dashboard.filters.allStatuses")}
            value={status}
            onChange={(next) => setStatus(next as AlertStatus[])}
            options={alertStatusFilterOptions(t)}
          />
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
          {closing && (
            <>
              <select
                className="select"
                aria-label={t("alerts.bulk.classificationLabel")}
                value={bulkClassification}
                onChange={(e) => setBulkClassification(e.target.value as Classification)}
                disabled={applying}
              >
                {BULK_CLASSIFICATIONS.map((c) => (
                  <option key={c} value={c}>
                    {t(`common.classification.${c}`)}
                  </option>
                ))}
              </select>
              <input
                className="input"
                style={{ minWidth: 220 }}
                aria-label={t("alerts.bulk.commentLabel")}
                placeholder={t("alerts.bulk.commentPlaceholder")}
                value={bulkComment}
                onChange={(e) => setBulkComment(e.target.value)}
                disabled={applying}
              />
            </>
          )}
          <button className="btn btn-primary btn-sm" onClick={applyBulkStatus} disabled={applying}>
            {applying ? t("alerts.bulk.applying") : closing ? t("alerts.bulk.applyClose") : t("alerts.bulk.apply")}
          </button>
          <button className="btn btn-ghost btn-sm" onClick={clear} disabled={applying}>
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
