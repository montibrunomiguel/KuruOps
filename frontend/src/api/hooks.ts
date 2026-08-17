import { useCallback, useEffect, useState } from "react";
import { useAuth, isSessionExpiredError } from "../auth/AuthContext";
import { ApiError } from "./client";
import i18n from "../i18n";

// resolveListError is useList/usePagedList's shared failure handling: a
// session-expiry error logs the user out (returning null -- the caller
// shouldn't also set an error state, since the app is about to redirect to
// login) rather than showing a raw error message the reload is about to
// wipe away anyway.
function resolveListError(err: unknown, logout: () => void): string | null {
  if (isSessionExpiredError(err)) {
    logout();
    return null;
  }
  return err instanceof ApiError ? err.message : String(i18n.t("common.loadFailed"));
}

interface ListState<T> {
  data: T[] | null;
  loading: boolean;
  error: string | null;
}

// useList fetches a GET endpoint that returns an array, and exposes reload()
// so panels can refresh after a mutation instead of hand-rolling their own
// fetch + loading/error state every time. On a 401 (expired/invalid token)
// it logs the session out so the app falls back to the login screen instead
// of showing a confusing permanent error state.
export function useList<T>(fetcher: (token: string | null) => Promise<T[]>, deps: unknown[] = []) {
  const { token, logout } = useAuth();
  const [state, setState] = useState<ListState<T>>({ data: null, loading: true, error: null });

  const reload = useCallback(() => {
    setState((s) => ({ ...s, loading: true, error: null }));
    fetcher(token)
      .then((data) => setState({ data, loading: false, error: null }))
      .catch((err: unknown) => {
        const message = resolveListError(err, logout);
        if (message === null) return;
        setState({ data: null, loading: false, error: message });
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token, ...deps]);

  useEffect(() => {
    reload();
  }, [reload]);

  return { ...state, reload };
}

interface ObjectState<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
}

// useObject mirrors useList but for a single-object GET endpoint -- same
// reload()/loading/error/401-logout shape, for Settings panels that load
// one config object (SMTP config, an identity provider, ...) rather than a
// list.
export function useObject<T>(fetcher: (token: string | null) => Promise<T>, deps: unknown[] = []) {
  const { token, logout } = useAuth();
  const [state, setState] = useState<ObjectState<T>>({ data: null, loading: true, error: null });

  const reload = useCallback(() => {
    setState((s) => ({ ...s, loading: true, error: null }));
    fetcher(token)
      .then((data) => setState({ data, loading: false, error: null }))
      .catch((err: unknown) => {
        const message = resolveListError(err, logout);
        if (message === null) return;
        setState({ data: null, loading: false, error: message });
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token, ...deps]);

  useEffect(() => {
    reload();
  }, [reload]);

  return { ...state, reload };
}

export const PAGE_SIZE_OPTIONS = [20, 40, 60, 100] as const;
export type PageSize = (typeof PAGE_SIZE_OPTIONS)[number];

interface PagedState<T> {
  items: T[];
  total: number;
  loading: boolean;
  error: string | null;
}

// usePagedList backs list pages (Alerts, Incidents) that could grow into the
// thousands in a real SOC instead of the handful mock data ships with --
// GET-everything-then-render doesn't scale. Unlike the older "load more"
// pattern this replaces, it's real page-number pagination: fetcher must
// return {items, total} (see api.getPaged, which reads the backend's
// X-Total-Count header), and page/pageSize are exposed so a <Pagination>
// component can drive them directly. Changing `deps` (e.g. a filter) or
// pageSize resets back to page 1, same as usePaginatedList did for offset 0.
export function usePagedList<T>(
  fetcher: (token: string | null, limit: number, offset: number) => Promise<{ items: T[]; total: number }>,
  deps: unknown[] = [],
) {
  const { token, logout } = useAuth();
  const [page, setPageState] = useState(1);
  const [pageSize, setPageSizeState] = useState<PageSize>(20);
  const [state, setState] = useState<PagedState<T>>({ items: [], total: 0, loading: true, error: null });

  const load = useCallback(
    (targetPage: number) => {
      setState((s) => ({ ...s, loading: true, error: null }));
      fetcher(token, pageSize, (targetPage - 1) * pageSize)
        .then(({ items, total }) => setState({ items, total, loading: false, error: null }))
        .catch((err: unknown) => {
          const message = resolveListError(err, logout);
          if (message === null) return;
          setState((s) => ({ ...s, loading: false, error: message }));
        });
    },
    // fetcher/logout deliberately excluded, same reasoning as reload()'s
    // useCallback above -- they're referenced by identity from the enclosing
    // component, but listing them would recreate load() (and re-fire the
    // effect below) on every render a caller passes a fresh inline fetcher.
    // Deliberately NOT keyed on `page` -- a filter/pageSize change should
    // always reset to page 1 (see the effect below), while navigating pages
    // goes through setPage() directly instead of via this effect, so the two
    // never race into a double-fetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [token, pageSize, ...deps],
  );

  useEffect(() => {
    setPageState(1);
    load(1);
  }, [load]);

  function setPage(p: number) {
    setPageState(p);
    load(p);
  }

  function setPageSize(size: PageSize) {
    setPageSizeState(size);
    // Resetting to page 1 and refetching happens via the effect above, since
    // pageSize is one of load()'s own deps.
  }

  return {
    ...state,
    page,
    pageSize,
    totalPages: Math.max(1, Math.ceil(state.total / pageSize)),
    setPage,
    setPageSize,
    reload: () => load(page),
  };
}

// mutationErrorMessage extracts a user-facing message from a failed
// create/update/delete call, for panels that manage their own submit state
// (forms) rather than going through useList.
export function mutationErrorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return String(i18n.t("common.saveFailed"));
}
