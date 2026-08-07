import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { useList, usePaginatedList, mutationErrorMessage } from "./hooks";
import { AuthProvider, useAuth } from "../auth/AuthContext";
import { ApiError } from "./client";

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

function withLoggedInSession() {
  localStorage.setItem(
    "argusops.session",
    JSON.stringify({
      token: "tok",
      user: { id: "1", email: "a@b.com", name: "A", role: "admin", mustChangePassword: false, resourceAccess: ["alerts"] },
    }),
  );
}

describe("useList", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("loads data on mount and exposes it once resolved", async () => {
    const fetcher = vi.fn().mockResolvedValue([{ id: 1 }, { id: 2 }]);
    const { result } = renderHook(() => useList(fetcher), { wrapper });

    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.data).toEqual([{ id: 1 }, { id: 2 }]);
    expect(result.current.error).toBeNull();
  });

  it("surfaces an ApiError's message on failure", async () => {
    const fetcher = vi.fn().mockRejectedValue(new ApiError(400, "bad filter"));
    const { result } = renderHook(() => useList(fetcher), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe("bad filter");
    expect(result.current.data).toBeNull();
  });

  it("falls back to a generic message for a non-ApiError failure", async () => {
    const fetcher = vi.fn().mockRejectedValue(new Error("network down"));
    const { result } = renderHook(() => useList(fetcher), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe("Falha ao carregar dados");
  });

  it("a 401 logs the session out instead of showing an error", async () => {
    withLoggedInSession();
    const fetcher = vi.fn().mockRejectedValue(new ApiError(401, "expired"));

    const { result } = renderHook(
      () => {
        const list = useList(fetcher);
        const auth = useAuth();
        return { list, auth };
      },
      { wrapper },
    );

    expect(result.current.auth.isAuthenticated).toBe(true);
    await waitFor(() => expect(result.current.auth.isAuthenticated).toBe(false));
    expect(result.current.list.error).toBeNull();
  });

  it("reload() re-invokes the fetcher", async () => {
    const fetcher = vi.fn().mockResolvedValue([]);
    const { result } = renderHook(() => useList(fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(fetcher).toHaveBeenCalledTimes(1);
    act(() => {
      result.current.reload();
    });
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  });
});

describe("usePaginatedList", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("fetches the first page on mount", async () => {
    withLoggedInSession();
    const fetcher = vi.fn().mockResolvedValue([{ id: 1 }]);
    const { result } = renderHook(() => usePaginatedList(fetcher), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(fetcher).toHaveBeenCalledWith("tok", 50, 0);
    expect(result.current.items).toEqual([{ id: 1 }]);
  });

  it("hasMore is true when a full page comes back, false for a short page", async () => {
    const fullPage = Array.from({ length: 50 }, (_, i) => ({ id: i }));
    const fetcher = vi.fn().mockResolvedValue(fullPage);
    const { result } = renderHook(() => usePaginatedList(fetcher), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.hasMore).toBe(true);

    const shortPage = Array.from({ length: 10 }, (_, i) => ({ id: i }));
    fetcher.mockResolvedValue(shortPage);
    act(() => {
      result.current.loadMore();
    });
    await waitFor(() => expect(result.current.loadingMore).toBe(false));
    expect(result.current.hasMore).toBe(false);
    expect(result.current.items).toHaveLength(60);
  });

  it("loadMore appends to existing items using the running offset", async () => {
    const fetcher = vi.fn();
    fetcher.mockResolvedValueOnce([{ id: 1 }, { id: 2 }]);
    const { result } = renderHook(() => usePaginatedList(fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));

    fetcher.mockResolvedValueOnce([{ id: 3 }]);
    act(() => {
      result.current.loadMore();
    });
    await waitFor(() => expect(result.current.loadingMore).toBe(false));

    expect(fetcher).toHaveBeenLastCalledWith(null, 50, 2);
    expect(result.current.items).toEqual([{ id: 1 }, { id: 2 }, { id: 3 }]);
  });

  it("reload() resets back to offset 0 and replaces items", async () => {
    const fetcher = vi.fn();
    fetcher.mockResolvedValueOnce([{ id: 1 }]);
    const { result } = renderHook(() => usePaginatedList(fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));

    fetcher.mockResolvedValueOnce([{ id: 99 }]);
    act(() => {
      result.current.reload();
    });
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(fetcher).toHaveBeenLastCalledWith(null, 50, 0);
    expect(result.current.items).toEqual([{ id: 99 }]);
  });

  it("a failure surfaces the ApiError message", async () => {
    const fetcher = vi.fn().mockRejectedValue(new ApiError(500, "server exploded"));
    const { result } = renderHook(() => usePaginatedList(fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe("server exploded");
  });

  it("a 401 logs the session out", async () => {
    withLoggedInSession();
    const fetcher = vi.fn().mockRejectedValue(new ApiError(401, "expired"));
    const { result } = renderHook(
      () => {
        const list = usePaginatedList(fetcher);
        const auth = useAuth();
        return { list, auth };
      },
      { wrapper },
    );
    await waitFor(() => expect(result.current.auth.isAuthenticated).toBe(false));
  });
});

describe("mutationErrorMessage", () => {
  it("extracts the ApiError message", () => {
    expect(mutationErrorMessage(new ApiError(400, "invalid tag"))).toBe("invalid tag");
  });

  it("falls back to a generic message for anything else", () => {
    expect(mutationErrorMessage(new Error("boom"))).toBe("Falha ao salvar");
    expect(mutationErrorMessage("a string")).toBe("Falha ao salvar");
  });
});
