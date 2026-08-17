// Thin fetch wrapper for /api/v1/**. Every call needs the caller's bearer
// token explicitly (no hidden global) so it's obvious at each call site
// that these endpoints are authenticated -- see useAuth() for where the
// token actually comes from.

import i18n from "../i18n";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = "ApiError";
  }
}

interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "DELETE";
  body?: unknown;
  token: string | null;
}

// refreshHandler is registered by AuthProvider (see auth/AuthContext.tsx) so
// this module -- which has no React state of its own -- can transparently
// retry a single 401 by exchanging the stored refresh token for a new
// access token via POST /auth/refresh, instead of every caller having to
// handle expiry itself. inFlight dedupes concurrent 401s into one refresh
// call: the backend rotates the refresh token on every use
// (AuthService.Refresh), so two parallel refresh attempts would race and
// the loser's rotated-away token would fail.
type RefreshHandler = () => Promise<string | null>;
let refreshHandler: RefreshHandler | null = null;
let inFlight: Promise<string | null> | null = null;

export function setRefreshHandler(fn: RefreshHandler | null) {
  refreshHandler = fn;
}

function refreshOnce(): Promise<string | null> {
  if (!refreshHandler) return Promise.resolve(null);
  if (!inFlight) {
    inFlight = refreshHandler().finally(() => {
      inFlight = null;
    });
  }
  return inFlight;
}

// parseErrorMessage extracts a user-facing message from a failed response:
// the backend's own {error: "..."} body when present, falling back to the
// HTTP status text, falling back to a generic translated message.
function parseErrorMessage(res: Response, payload: unknown): string {
  const backendMessage = payload && typeof payload === "object" && "error" in payload ? String(payload.error) : "";
  return backendMessage || res.statusText || String(i18n.t("common.unexpectedError"));
}

// fetchWithAuth is request/requestPaged's shared core: attaches the bearer
// token and transparently retries once on a 401 after a token refresh (see
// refreshOnce above). Only authenticated requests (token set) are eligible
// -- login/refresh itself pass token: null, so this can't loop back into
// refreshing off of a refresh failure.
async function fetchWithAuth(path: string, init: RequestInit, token: string | null, isRetry = false): Promise<Response> {
  const res = await fetch(path, {
    ...init,
    headers: { ...init.headers, ...(token ? { Authorization: `Bearer ${token}` } : {}) },
  });

  if (res.status === 401 && !isRetry && token) {
    const newToken = await refreshOnce();
    if (newToken) {
      return fetchWithAuth(path, init, newToken, true);
    }
  }

  return res;
}

async function request<T>(path: string, opts: RequestOptions): Promise<T> {
  const res = await fetchWithAuth(
    path,
    {
      method: opts.method ?? "GET",
      headers: opts.body !== undefined ? { "Content-Type": "application/json" } : {},
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    },
    opts.token,
  );

  if (res.status === 204) {
    return undefined as T;
  }

  const isJson = res.headers.get("content-type")?.includes("application/json");
  const payload = isJson ? await res.json().catch(() => undefined) : undefined;

  if (!res.ok) {
    throw new ApiError(res.status, parseErrorMessage(res, payload));
  }

  return payload as T;
}

// requestPaged mirrors request<T[]>, but also reads the X-Total-Count
// response header (see backend/internal/httpserver/handlers/respond.go's
// callers) -- kept as a separate function rather than growing request<T>'s
// return shape, since every other caller of request<T> expects a bare body
// and would otherwise need updating to unwrap {items, total}.
async function requestPaged<T>(path: string, opts: RequestOptions): Promise<{ items: T[]; total: number }> {
  const res = await fetchWithAuth(path, { method: opts.method ?? "GET" }, opts.token);

  const isJson = res.headers.get("content-type")?.includes("application/json");
  const payload = isJson ? await res.json().catch(() => undefined) : undefined;

  if (!res.ok) {
    throw new ApiError(res.status, parseErrorMessage(res, payload));
  }

  const items = (payload as T[] | undefined) ?? [];
  const totalHeader = res.headers.get("X-Total-Count");
  const total = totalHeader !== null ? Number(totalHeader) : items.length;
  return { items, total };
}

export const api = {
  get: <T>(path: string, token: string | null) => request<T>(path, { token }),
  getPaged: <T>(path: string, token: string | null) => requestPaged<T>(path, { token }),
  post: <T>(path: string, body: unknown, token: string | null) =>
    request<T>(path, { method: "POST", body, token }),
  put: <T>(path: string, body: unknown, token: string | null) =>
    request<T>(path, { method: "PUT", body, token }),
  del: <T>(path: string, token: string | null) => request<T>(path, { method: "DELETE", token }),
  // kind/id identify the alert or incident the evidence is attached to --
  // the backend uses them to build the storage key
  // (<Alert|Incident>/yyyy/mm/dd/Title/uuid_file.ext) and to enforce the
  // same tag-visibility rule every other alert/incident endpoint does. Any
  // file type is accepted here -- the backend is what actually enforces
  // what's allowed (see UploadHandlers.resolveAttachmentExt).
  uploadAttachment: async (
    file: File,
    kind: "alert" | "incident",
    id: string,
    token: string | null,
  ): Promise<{ url: string }> => {
    const form = new FormData();
    form.append("file", file);
    form.append("kind", kind);
    form.append("id", id);
    // No Content-Type header here -- the browser sets multipart/form-data
    // with the right boundary itself; setting it manually drops the
    // boundary and the server can't parse the form.
    const res = await fetch("/api/v1/uploads/images", {
      method: "POST",
      headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}) },
      body: form,
    });
    const payload = await res.json().catch(() => undefined);
    if (!res.ok) {
      throw new ApiError(res.status, parseErrorMessage(res, payload));
    }
    return payload as { url: string };
  },
  // For endpoints that respond with a downloadable file (Content-Disposition:
  // attachment) rather than JSON -- e.g. the CEF audit export. request<T>
  // above assumes a JSON body, so this bypasses it entirely rather than
  // trying to make one function handle both shapes.
  downloadFile: async (path: string, token: string | null): Promise<{ blob: Blob; filename: string }> => {
    const res = await fetch(path, {
      headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    });
    if (!res.ok) {
      const payload = await res.json().catch(() => undefined);
      throw new ApiError(res.status, parseErrorMessage(res, payload));
    }
    const disposition = res.headers.get("content-disposition") ?? "";
    const match = /filename="([^"]+)"/.exec(disposition);
    return { blob: await res.blob(), filename: match ? match[1] : "download" };
  },
};
