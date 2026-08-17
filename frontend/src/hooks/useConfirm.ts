import { useState } from "react";

// Inline confirm/cancel state, reimplemented from scratch in ~15 Settings
// panels before this -- native window.confirm() is avoided because some
// embedded browser contexts silently auto-dismiss it, which made delete
// look like it does nothing (see OnCallScheduleDetailPage's original
// comment on this). Defaults to a single global boolean confirmation
// (`useConfirm()`); pass a type param for a per-row variant scoped by id
// (`useConfirm<string>()`, then `confirm(row.id)` / `confirming === row.id`).
export function useConfirm<TId = true>() {
  const [confirming, setConfirming] = useState<TId | null>(null);

  function confirm(id: TId = true as TId) {
    setConfirming(id);
  }

  function cancel() {
    setConfirming(null);
  }

  return { confirming, confirm, cancel };
}
