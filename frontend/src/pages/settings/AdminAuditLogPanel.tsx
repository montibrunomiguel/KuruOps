import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth, isSessionExpiredError } from "../../auth/AuthContext";
import { api, ApiError } from "../../api/client";
import { formatDateTime } from "../../lib/format";
import type { AdminAuditLogCursor, AdminAuditLogEntry, AdminAuditLogPage } from "../../types/api";

const PAGE_SIZE = 50;

function auditLogPath(cursor: AdminAuditLogCursor | null): string {
  const params = new URLSearchParams({ limit: String(PAGE_SIZE) });
  if (cursor) {
    params.set("beforeCreatedAt", cursor.createdAt);
    params.set("beforeId", String(cursor.id));
  }
  return `/api/v1/settings/audit-log?${params.toString()}`;
}

// Settings -> Data & Audit -> Audit Log: every Settings change any admin has
// made (see domain.AdminAuditEvent) -- who, what area, what action, and the
// before/after diff. Read-only and append-only: nothing here is editable or
// deletable, matching the underlying table. Keyset "load more" pagination
// (not page numbers, see AdminAuditLogService.List) since new rows only
// ever land at the top, so a stable cursor never skips or repeats a row the
// way an offset would if someone saved a setting between two page loads.
export function AdminAuditLogPanel() {
  const { t } = useTranslation();
  const { token, logout } = useAuth();
  const [entries, setEntries] = useState<AdminAuditLogEntry[]>([]);
  const [nextCursor, setNextCursor] = useState<AdminAuditLogCursor | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [expandedIds, setExpandedIds] = useState<Set<number>>(new Set());

  async function load(cursor: AdminAuditLogCursor | null) {
    if (cursor) {
      setLoadingMore(true);
    } else {
      setLoading(true);
    }
    setError(null);
    try {
      const page = await api.get<AdminAuditLogPage>(auditLogPath(cursor), token);
      setEntries((prev) => (cursor ? [...prev, ...page.events] : page.events));
      setNextCursor(page.nextCursor);
    } catch (err) {
      if (isSessionExpiredError(err)) {
        logout();
        return;
      }
      setError(err instanceof ApiError ? err.message : String(t("common.loadFailed")));
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }

  useEffect(() => {
    void load(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function toggleExpanded(id: number) {
    setExpandedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.auditLog.title")}</h2>
      </div>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.auditLog.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && entries.length === 0 && !error && <div className="empty-state">{t("settings.auditLog.empty")}</div>}

      {!loading && entries.length > 0 && (
        <>
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>{t("settings.auditLog.table.when")}</th>
                  <th>{t("settings.auditLog.table.area")}</th>
                  <th>{t("settings.auditLog.table.action")}</th>
                  <th>{t("settings.auditLog.table.actor")}</th>
                  <th>{t("settings.auditLog.table.diff")}</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry) => (
                  <tr key={entry.id}>
                    <td className="mono">{formatDateTime(entry.createdAt)}</td>
                    <td>
                      <span className="badge badge-muted">{entry.area}</span>
                    </td>
                    <td>{entry.action}</td>
                    <td className="table-sub-cell">{entry.actorName || t("settings.auditLog.unknownActor")}</td>
                    <td>
                      <button className="btn btn-ghost btn-sm" onClick={() => toggleExpanded(entry.id)}>
                        {expandedIds.has(entry.id) ? t("alerts.detail.collapse") : t("alerts.detail.expand")}
                      </button>
                      {expandedIds.has(entry.id) && (
                        <pre
                          style={{
                            margin: "8px 0 0",
                            fontSize: 11.5,
                            fontFamily: "IBM Plex Mono, monospace",
                            whiteSpace: "pre-wrap",
                            wordBreak: "break-word",
                            color: "var(--text-secondary)",
                          }}
                        >
                          {JSON.stringify(entry.data, null, 2)}
                        </pre>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {nextCursor && (
            <div className="row-actions" style={{ marginTop: 12 }}>
              <button className="btn btn-secondary btn-sm" onClick={() => load(nextCursor)} disabled={loadingMore}>
                {loadingMore ? t("common.loading") : t("settings.auditLog.loadMore")}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
