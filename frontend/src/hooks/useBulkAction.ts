import { useState } from "react";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";
import type { BulkResponse } from "../types/api";

// The "POST a bulk endpoint, tally per-item results, surface a
// success/failed summary" state machine shared by AlertsListPage's
// bulk-status and IncidentsListPage's bulk-phase toolbars -- extracted
// after both had independently grown the same applying/error/summary
// state and success/failed tally over BulkResponse.results. A partial
// failure (e.g. a row the caller lost tag-based visibility to mid-flight)
// is not an error from this hook's perspective -- it's a summary to show,
// same as the two list pages already treated it before this was factored
// out.
export function useBulkAction(endpoint: string, token: string | null) {
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [summary, setSummary] = useState<{ success: number; failed: number } | null>(null);

  // extra carries whatever the endpoint needs beyond `ids` -- {status} for
  // /alerts/bulk/status, {phase} for /incidents/bulk/phase. onSuccess is
  // the caller's own post-apply cleanup (clear its row selection, reload
  // its list) -- kept as a callback rather than owned here since selection
  // state lives in the caller's own useRowSelection instance, not this hook.
  async function apply(ids: string[], extra: Record<string, unknown>, onSuccess: () => void) {
    setApplying(true);
    setError(null);
    setSummary(null);
    try {
      const resp = await api.post<BulkResponse>(endpoint, { ids, ...extra }, token);
      const success = resp.results.filter((r) => r.success).length;
      const failed = resp.results.length - success;
      setSummary({ success, failed });
      onSuccess();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setApplying(false);
    }
  }

  return { applying, error, summary, apply };
}
