import { useEffect, useState } from "react";

// Delays reflecting a fast-changing value (e.g. a search box driving a
// server-side query) until it's stopped changing for `delayMs` -- without
// this, AlertsListPage/IncidentsListPage's full-text search would fire one
// request per keystroke. Every other text filter in these list pages
// (source/tag) is an exact-match filter cheap enough to run un-debounced;
// full-text search is the first one worth the extra state.
export function useDebouncedValue<T>(value: T, delayMs = 300): T {
  const [debounced, setDebounced] = useState(value);

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);

  return debounced;
}
