import { useEffect, useState } from "react";

// useNow returns the current time in milliseconds and refreshes it every
// intervalMs, so a component that shows "2h ago" or "SLA in 14m" can read the
// clock without calling Date.now() while rendering (an impure read that gives
// a different answer on every render) and keeps its text current without a
// reload. The first value is taken when the component mounts.
export function useNow(intervalMs = 60_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}
