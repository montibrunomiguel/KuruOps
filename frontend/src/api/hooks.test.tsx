import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { useList, usePagedList, mutationErrorMessage } from "./hooks";
import { AuthProvider, useAuth } from "../auth/AuthContext";
import { ApiError } from "./client";

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

function withLoggedInSession() {
  localStorage.setItem(
    "kuruops.session",
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
    const { result } = renderHook(() => useList(["test-list-1"], fetcher), { wrapper });

    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.data).toEqual([{ id: 1 }, { id: 2 }]);
    expect(result.current.error).toBeNull();
  });

  it("surfaces an ApiError's message on failure", async () => {
    const fetcher = vi.fn().mockRejectedValue(new ApiError(400, "bad filter"));
    const { result } = renderHook(() => useList(["test-list-2"], fetcher), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe("bad filter");
    expect(result.current.data).toBeNull();
  });

  it("falls back to a generic message for a non-ApiError failure", async () => {
    const fetcher = vi.fn().mockRejectedValue(new Error("network down"));
    const { result } = renderHook(() => useList(["test-list-3"], fetcher), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe("Failed to load data");
  });

  it("a 401 logs the session out instead of showing an error", async () => {
    withLoggedInSession();
    const fetcher = vi.fn().mockRejectedValue(new ApiError(401, "expired"));

    const { result } = renderHook(
      () => {
        const list = useList(["test-list-401"], fetcher);
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
    const { result } = renderHook(() => useList(["test-list-reload"], fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(fetcher).toHaveBeenCalledTimes(1);
    act(() => {
      result.current.reload();
    });
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  });
});

describe("usePagedList", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("fetches page 1 with the default page size (20) on mount", async () => {
    withLoggedInSession();
    const fetcher = vi.fn().mockResolvedValue({ items: [{ id: 1 }], total: 1 });
    const { result } = renderHook(() => usePagedList(["test-paged-1"], fetcher), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(fetcher).toHaveBeenCalledWith("tok", 20, 0);
    expect(result.current.items).toEqual([{ id: 1 }]);
    expect(result.current.page).toBe(1);
    expect(result.current.pageSize).toBe(20);
    expect(result.current.total).toBe(1);
  });

  it("totalPages is computed from total/pageSize, minimum 1", async () => {
    const fetcher = vi.fn().mockResolvedValue({ items: [], total: 45 });
    const { result } = renderHook(() => usePagedList(["test-paged-2"], fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.totalPages).toBe(3); // ceil(45/20)

    fetcher.mockResolvedValue({ items: [], total: 0 });
    act(() => {
      result.current.reload();
    });
    await waitFor(() => expect(result.current.total).toBe(0));
    expect(result.current.totalPages).toBe(1);
  });

  it("setPage fetches the requested page's offset", async () => {
    const fetcher = vi.fn().mockResolvedValue({ items: [{ id: 1 }], total: 100 });
    const { result } = renderHook(() => usePagedList(["test-paged-3"], fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));

    fetcher.mockResolvedValueOnce({ items: [{ id: 2 }], total: 100 });
    act(() => {
      result.current.setPage(3);
    });
    await waitFor(() => expect(result.current.page).toBe(3));
    // page flips synchronously on setPage(), but the new page's data is a
    // separate async fetch -- wait for it rather than assuming it landed in
    // the same tick as the page-number update.
    await waitFor(() => expect(result.current.items).toEqual([{ id: 2 }]));

    expect(fetcher).toHaveBeenLastCalledWith(null, 20, 40);
  });

  it("setPageSize resets to page 1 and refetches with the new limit", async () => {
    const fetcher = vi.fn().mockResolvedValue({ items: [], total: 100 });
    const { result } = renderHook(() => usePagedList(["test-paged-4"], fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.setPage(3);
    });
    await waitFor(() => expect(result.current.page).toBe(3));

    act(() => {
      result.current.setPageSize(100);
    });
    await waitFor(() => expect(result.current.pageSize).toBe(100));

    expect(result.current.page).toBe(1);
    expect(fetcher).toHaveBeenLastCalledWith(null, 100, 0);
  });

  it("reload() re-fetches the current page, not page 1", async () => {
    const fetcher = vi.fn().mockResolvedValue({ items: [{ id: 1 }], total: 100 });
    const { result } = renderHook(() => usePagedList(["test-paged-5"], fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.setPage(2);
    });
    await waitFor(() => expect(result.current.page).toBe(2));

    fetcher.mockResolvedValueOnce({ items: [{ id: 99 }], total: 100 });
    act(() => {
      result.current.reload();
    });
    await waitFor(() => expect(result.current.items).toEqual([{ id: 99 }]));

    expect(fetcher).toHaveBeenLastCalledWith(null, 20, 20);
  });

  it("resets to page 1 when the caller's queryKey prefix changes", async () => {
    const fetcher = vi.fn().mockResolvedValue({ items: [{ id: 1 }], total: 100 });
    const { result, rerender } = renderHook(({ key }: { key: string }) => usePagedList([key], fetcher), {
      wrapper,
      initialProps: { key: "filter-a" },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.setPage(3);
    });
    await waitFor(() => expect(result.current.page).toBe(3));

    rerender({ key: "filter-b" });
    await waitFor(() => expect(result.current.page).toBe(1));
  });

  it("a failure surfaces the ApiError message", async () => {
    const fetcher = vi.fn().mockRejectedValue(new ApiError(500, "server exploded"));
    const { result } = renderHook(() => usePagedList(["test-paged-error"], fetcher), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe("server exploded");
  });

  it("a 401 logs the session out", async () => {
    withLoggedInSession();
    const fetcher = vi.fn().mockRejectedValue(new ApiError(401, "expired"));
    const { result } = renderHook(
      () => {
        const list = usePagedList(["test-paged-401"], fetcher);
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
    expect(mutationErrorMessage(new Error("boom"))).toBe("Failed to save");
    expect(mutationErrorMessage("a string")).toBe("Failed to save");
  });
});
