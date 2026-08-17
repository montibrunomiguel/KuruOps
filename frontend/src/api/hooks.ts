import { useEffect, useState } from "react";
import { useQuery, type QueryKey } from "@tanstack/react-query";
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

// useList fetches a GET endpoint that returns an array, and exposes reload()
// so panels can refresh after a mutation instead of hand-rolling their own
// fetch + loading/error state every time. On a 401 (expired/invalid token)
// it logs the session out so the app falls back to the login screen instead
// of showing a confusing permanent error state.
//
// Backed by react-query's useQuery -- queryKey identifies this query in the
// shared cache (giving free request de-duping and stale-response-safe
// refetches), while the returned shape ({data, loading, error, reload}) is
// kept identical to the old hand-rolled hook so call sites don't need to
// change beyond passing a queryKey.
export function useList<T>(queryKey: QueryKey, fetcher: (token: string | null) => Promise<T[]>) {
  const { token, logout } = useAuth();
  const query = useQuery<T[]>({
    queryKey,
    queryFn: () => fetcher(token),
    retry: false,
  });

  useEffect(() => {
    if (query.error) resolveListError(query.error, logout);
  }, [query.error, logout]);

  const message = query.error ? resolveListError(query.error, () => {}) : null;

  return {
    data: query.data ?? null,
    loading: query.isLoading,
    error: message,
    reload: query.refetch,
  };
}

// useObject mirrors useList but for a single-object GET endpoint -- same
// reload()/loading/error/401-logout shape, for Settings panels that load
// one config object (SMTP config, an identity provider, ...) rather than a
// list.
export function useObject<T>(queryKey: QueryKey, fetcher: (token: string | null) => Promise<T>) {
  const { token, logout } = useAuth();
  const query = useQuery<T>({
    queryKey,
    queryFn: () => fetcher(token),
    retry: false,
  });

  useEffect(() => {
    if (query.error) resolveListError(query.error, logout);
  }, [query.error, logout]);

  const message = query.error ? resolveListError(query.error, () => {}) : null;

  return {
    data: query.data ?? null,
    loading: query.isLoading,
    error: message,
    reload: query.refetch,
  };
}

export const PAGE_SIZE_OPTIONS = [20, 40, 60, 100] as const;
export type PageSize = (typeof PAGE_SIZE_OPTIONS)[number];

// usePagedList backs list pages (Alerts, Incidents) that could grow into the
// thousands in a real SOC instead of the handful mock data ships with --
// GET-everything-then-render doesn't scale. Unlike the older "load more"
// pattern this replaces, it's real page-number pagination: fetcher must
// return {items, total} (see api.getPaged, which reads the backend's
// X-Total-Count header), and page/pageSize are exposed so a <Pagination>
// component can drive them directly. Changing the caller's `queryKey`
// prefix (e.g. a filter) or pageSize resets back to page 1, same as
// usePaginatedList did for offset 0.
export function usePagedList<T>(
  queryKey: QueryKey,
  fetcher: (token: string | null, limit: number, offset: number) => Promise<{ items: T[]; total: number }>,
) {
  const { token, logout } = useAuth();
  const [page, setPageState] = useState(1);
  const [pageSize, setPageSizeState] = useState<PageSize>(20);

  // Reset to page 1 whenever the caller's queryKey prefix changes -- mirrors
  // the old hook's "changing deps resets to page 1" behavior. Stringify for
  // a stable dependency since queryKey is a fresh array/objects each render.
  const keyString = JSON.stringify(queryKey);
  useEffect(() => {
    setPageState(1);
  }, [keyString]);

  const fullKey = [...queryKey, page, pageSize];

  const query = useQuery<{ items: T[]; total: number }>({
    queryKey: fullKey,
    queryFn: () => fetcher(token, pageSize, (page - 1) * pageSize),
    retry: false,
  });

  useEffect(() => {
    if (query.error) resolveListError(query.error, logout);
  }, [query.error, logout]);

  const message = query.error ? resolveListError(query.error, () => {}) : null;

  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;

  function setPage(p: number) {
    setPageState(p);
  }

  function setPageSize(size: PageSize) {
    setPageSizeState(size);
    setPageState(1);
  }

  return {
    items,
    total,
    loading: query.isLoading,
    error: message,
    page,
    pageSize,
    totalPages: Math.max(1, Math.ceil(total / pageSize)),
    setPage,
    setPageSize,
    reload: () => query.refetch(),
  };
}

// mutationErrorMessage extracts a user-facing message from a failed
// create/update/delete call, for panels that manage their own submit state
// (forms) rather than going through useList.
export function mutationErrorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return String(i18n.t("common.saveFailed"));
}
