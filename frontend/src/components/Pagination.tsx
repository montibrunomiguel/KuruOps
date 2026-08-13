import { useTranslation } from "react-i18next";
import { PAGE_SIZE_OPTIONS, type PageSize } from "../api/hooks";

// Presentational pagination bar for usePagedList-backed pages (Alerts,
// Incidents) -- page-size select (20/40/60/100) + prev/next, driven entirely
// by props so the page component keeps ownership of the actual fetch state.
export function Pagination({
  page,
  pageSize,
  total,
  totalPages,
  onPageChange,
  onPageSizeChange,
}: {
  page: number;
  pageSize: PageSize;
  total: number;
  totalPages: number;
  onPageChange: (page: number) => void;
  onPageSizeChange: (size: PageSize) => void;
}) {
  const { t } = useTranslation();
  if (total === 0) return null;

  const start = (page - 1) * pageSize + 1;
  const end = Math.min(page * pageSize, total);

  return (
    <div
      className="pagination-bar"
      style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 14, gap: 12, flexWrap: "wrap" }}
    >
      <span className="chart-card-sub">{t("common.pagination.showing", { start, end, total })}</span>
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <select
          className="select"
          aria-label={t("common.pagination.pageSizeLabel")}
          value={pageSize}
          onChange={(e) => onPageSizeChange(Number(e.target.value) as PageSize)}
        >
          {PAGE_SIZE_OPTIONS.map((size) => (
            <option key={size} value={size}>
              {t("common.pagination.perPage", { count: size })}
            </option>
          ))}
        </select>
        <button className="btn btn-sm" onClick={() => onPageChange(page - 1)} disabled={page <= 1}>
          {t("common.pagination.previous")}
        </button>
        <span className="chart-card-sub">{t("common.pagination.pageIndicator", { page, totalPages })}</span>
        <button className="btn btn-sm" onClick={() => onPageChange(page + 1)} disabled={page >= totalPages}>
          {t("common.pagination.next")}
        </button>
      </div>
    </div>
  );
}
