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
  // filters), so it is compared by hand instead of as an effect dependency list.
  // The reset happens while rendering (React's "reset state when an input
  // changes" pattern), not in an effect: an effect would render once with the
  // new filter and the old selection, briefly showing a bulk toolbar count for
  // rows the caller can no longer see.
  const [prevDeps, setPrevDeps] = useState(resetDeps);
  if (prevDeps.length !== resetDeps.length || resetDeps.some((d, i) => !Object.is(d, prevDeps[i]))) {
    setPrevDeps(resetDeps);
    setSelected(new Set());
  }

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
