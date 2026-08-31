import { useEffect, useRef, useState, type DependencyList } from "react";

// The "checkbox column + select-all" state machine shared by
// AlertsListPage/IncidentsListPage's bulk-action toolbars -- extracted here
// after both pages had independently grown the exact same ~25 lines
// (selected Set, toggleRow, toggleSelectAll, the allOnPageSelected/
// someOnPageSelected pair driving the header checkbox's indeterminate
// state, and a reset-on-filter-change effect).
//
// Selection is deliberately scoped to what's currently on screen, not
// persisted across a page/filter change -- resetDeps is the list page's
// own filter/pagination state (severity, status, page, ...), passed
// through verbatim to an internal effect so the bulk toolbar's count never
// refers to a row the caller can no longer see once any of those change.
export function useRowSelection<T>(items: T[], getId: (item: T) => string, resetDeps: DependencyList) {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  // resetDeps is caller-supplied and inherently dynamic per page (different
  // filters), so exhaustive-deps can't verify it statically -- same
  // established pattern as AnalysisChat.tsx/AdminAuditLogPanel.tsx.
  useEffect(() => {
    setSelected(new Set());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, resetDeps);

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

  const allOnPageSelected = items.length > 0 && items.every((item) => selected.has(getId(item)));
  const someOnPageSelected = items.some((item) => selected.has(getId(item)));
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
      setSelected(new Set(items.map(getId)));
    }
  }

  function clear() {
    setSelected(new Set());
  }

  return { selected, toggleRow, toggleSelectAll, allOnPageSelected, someOnPageSelected, selectAllRef, clear };
}
