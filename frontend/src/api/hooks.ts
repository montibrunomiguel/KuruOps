import { useCallback, useEffect, useRef, useState } from "react";
import { useAuth, isSessionExpiredError } from "../auth/AuthContext";
import { ApiError } from "./client";

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
        if (isSessionExpiredError(err)) {
          logout();
          return;
        }
        const message = err instanceof ApiError ? err.message : "Falha ao carregar dados";
        setState({ data: null, loading: false, error: message });
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token, ...deps]);

  useEffect(() => {
    reload();
  }, [reload]);

  return { ...state, reload };
}

const PAGE_SIZE = 50;

interface PaginatedState<T> {
  items: T[];
  loading: boolean;
  loadingMore: boolean;
  error: string | null;
  hasMore: boolean;
}

// usePaginatedList backs list pages (Alerts, Incidents) that could grow into
// the thousands in a real SOC instead of the handful mock data ships with --
// GET-everything-then-render doesn't scale, so this fetches PAGE_SIZE rows
// at a time via the backend's limit/offset support (see parsePaging in
// backend/internal/httpserver/handlers/respond.go) and exposes loadMore()
// for a "Carregar mais" button. Changing `deps` (e.g. a filter) resets back
// to the first page, same as useList.
export function usePaginatedList<T>(
  fetcher: (token: string | null, limit: number, offset: number) => Promise<T[]>,
  deps: unknown[] = [],
) {
  const { token, logout } = useAuth();
  const [state, setState] = useState<PaginatedState<T>>({
    items: [],
    loading: true,
    loadingMore: false,
    error: null,
    hasMore: false,
  });
  // Mirrors state.items.length without needing to be a dependency of load()
  // -- avoids load() being recreated (and effects re-firing) on every page
  // fetched, which a plain items.length dependency would cause.
  const offsetRef = useRef(0);

  const load = useCallback(
    (reset: boolean) => {
      const offset = reset ? 0 : offsetRef.current;
      setState((s) => ({ ...s, loading: reset, loadingMore: !reset, error: null }));
      fetcher(token, PAGE_SIZE, offset)
        .then((rows) => {
          offsetRef.current = offset + rows.length;
          setState((s) => ({
            items: reset ? rows : [...s.items, ...rows],
            loading: false,
            loadingMore: false,
            error: null,
            hasMore: rows.length === PAGE_SIZE,
          }));
        })
        .catch((err: unknown) => {
          if (isSessionExpiredError(err)) {
            logout();
            return;
          }
          const message = err instanceof ApiError ? err.message : "Falha ao carregar dados";
          setState((s) => ({ ...s, loading: false, loadingMore: false, error: message }));
        });
    },
    // fetcher/logout deliberately excluded, same reasoning as reload()'s
    // useCallback above -- they're referenced by identity from the enclosing
    // component, but listing them would recreate load() (and re-fire the
    // effect below) on every render a caller passes a fresh inline fetcher.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [token, ...deps],
  );

  useEffect(() => {
    offsetRef.current = 0;
    load(true);
  }, [load]);

  return { ...state, loadMore: () => load(false), reload: () => load(true) };
}

// mutationErrorMessage extracts a user-facing message from a failed
// create/update/delete call, for panels that manage their own submit state
// (forms) rather than going through useList.
export function mutationErrorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return "Falha ao salvar";
}
