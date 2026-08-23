import { useState } from "react";

// The "show a success message after a save" boolean, reimplemented from
// scratch in 6 Settings panels before this (Retention/Storage/SMTP/
// Incident-SLA/Identity-Providers' LDAP+SAML/Profile) -- each its own
// `useState(false)` plus a manual set-true-on-success/clear-at-top-of-save
// pair. Sticky by design, matching every one of those existing call sites:
// once shown, a saved message stays until the next save attempt clears it
// (no auto-dismiss timer) -- an admin scrolling back up after saving should
// still see confirmation it worked.
//
// `initial` exists only for Storage's OAuth-redirect case (its `saved`
// state seeds from a `?gdrive_connected=1` query param on mount, not from a
// same-session save) -- everything else can ignore it.
export function useSavedFlag(initial = false) {
  const [saved, setSaved] = useState(initial);

  function markSaved() {
    setSaved(true);
  }

  function clearSaved() {
    setSaved(false);
  }

  return { saved, markSaved, clearSaved };
}
