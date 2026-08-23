import { useState } from "react";
import type { QueryKey } from "@tanstack/react-query";
import { useObject, mutationErrorMessage } from "../api/hooks";
import { useSavedFlag } from "./useSavedFlag";

// Bundles the "GET a single config object (or null if unconfigured), edit
// local form state seeded from it, mutate to save/remove, reload after
// success" shape reimplemented from scratch across Slack/Retention/SMTP/
// Storage/Identity-Providers' LDAP+SAML -- 6 near-identical copies of
// submitting/saveError/saved state plus a try/mutationErrorMessage/finally
// block around whatever the actual api.put/api.del call is.
//
// Deliberately takes the mutation itself as a caller-supplied callback
// (`save(mutate)`) rather than a fixed endpoint/payload -- Storage alone
// branches across 3 different PUT endpoints (S3/GCS/GDrive-service-account)
// depending on which provider tab is active, and Slack's only mutation is a
// DELETE (disconnect), not a PUT -- a hook that only knew how to PUT one
// fixed URL couldn't fit either. What's shared instead is the *bookkeeping*
// around whatever request the caller makes: submitting/error/saved state,
// clearing saved+error at the start, reload() after success. Some panels
// (Storage's OAuth-connect branch, Slack's connect flow) don't call save()
// at all -- they redirect via window.location.href instead -- and just use
// this hook's GET/reload/configured half.
//
// `configured` fixes a real bug present in every one of the 6 original
// call sites: each derived "not configured yet" from bare truthiness of
// the fetched object, but useObject's `data` collapses to null both when
// the GET legitimately returned null AND when the GET failed -- so a
// load failure showed the "Connect"/"Configure" CTA as if nothing were set
// up, with only an easy-to-miss error banner alongside it as the only
// sign anything was actually wrong. `configured` is only ever true once
// loading and error have both resolved cleanly, so callers can gate the
// unconfigured-state CTA on it instead of on `data` directly.
// `initialSaved` exists only for Storage/Slack's OAuth-redirect case: their
// `saved` state needs to seed from a `?gdrive_connected=1`-style query
// param read once on mount, not from a same-session save -- everything
// else can omit it.
export function useAdminSingletonConfig<T>(
  queryKey: QueryKey,
  fetcher: (token: string | null) => Promise<T>,
  options: { initialSaved?: boolean } = {},
) {
  const { data, loading, error, reload } = useObject<T>(queryKey, fetcher);
  const [submitting, setSubmitting] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const { saved, markSaved, clearSaved } = useSavedFlag(options.initialSaved ?? false);

  const configured = !loading && !error && data !== null;

  // markSavedOnSuccess defaults to true (the PUT/save case); a remove/
  // delete mutation still wants the same shared submitting/error/reload
  // bookkeeping (so its buttons disable together with the save button, same
  // as every original hand-rolled panel did) but passes false here -- a
  // "Configuration saved!" message after deleting the configuration would
  // be actively misleading.
  async function save(mutate: () => Promise<void>, opts: { markSavedOnSuccess?: boolean } = {}) {
    const { markSavedOnSuccess = true } = opts;
    setSubmitting(true);
    setSaveError(null);
    clearSaved();
    try {
      await mutate();
      if (markSavedOnSuccess) markSaved();
      reload();
    } catch (err) {
      setSaveError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return { data, loading, error, reload, configured, submitting, saveError, setSaveError, saved, clearSaved, save };
}
