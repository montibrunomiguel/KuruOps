import { describe, it, expect, vi } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { AuthProvider } from "../auth/AuthContext";
import { api } from "../api/client";
import { useAdminSingletonConfig } from "./useAdminSingletonConfig";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

interface FakeConfig {
  host: string;
}

describe("useAdminSingletonConfig", () => {
  it("configured is true once a real object loads successfully", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ host: "smtp.example" })));

    const { result } = renderHook(
      () => useAdminSingletonConfig<FakeConfig | null>(["fake-config"], (tok) => api.get("/api/v1/fake-config", tok)),
      { wrapper: AuthProvider },
    );

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.data).toEqual({ host: "smtp.example" });
    expect(result.current.configured).toBe(true);
  });

  it("configured is false when the GET succeeds but returns null (genuinely unconfigured)", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));

    const { result } = renderHook(
      () => useAdminSingletonConfig<FakeConfig | null>(["fake-config"], (tok) => api.get("/api/v1/fake-config", tok)),
      { wrapper: AuthProvider },
    );

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBeNull();
    expect(result.current.configured).toBe(false);
  });

  it("configured is false (not true) when the GET fails -- the bug every hand-rolled panel had", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "boom" }, 500)));

    const { result } = renderHook(
      () => useAdminSingletonConfig<FakeConfig | null>(["fake-config"], (tok) => api.get("/api/v1/fake-config", tok)),
      { wrapper: AuthProvider },
    );

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe("boom");
    // The bug this hook fixes: data collapses to null on both "loaded,
    // genuinely unconfigured" and "load failed" -- configured must only be
    // true for the former, distinguishing the two via `error`.
    expect(result.current.configured).toBe(false);
  });

  it("seeds saved from options.initialSaved -- for a caller reading a one-time OAuth-redirect query param", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ host: "smtp.example" })));

    const { result } = renderHook(
      () =>
        useAdminSingletonConfig<FakeConfig | null>(["fake-config"], (tok) => api.get("/api/v1/fake-config", tok), {
          initialSaved: true,
        }),
      { wrapper: AuthProvider },
    );

    expect(result.current.saved).toBe(true);
  });

  it("save() runs the mutation, marks saved, clears saveError, and reloads", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse({ host: "smtp.example" }));
    });
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(
      () => useAdminSingletonConfig<FakeConfig | null>(["fake-config"], (tok) => api.get("/api/v1/fake-config", tok)),
      { wrapper: AuthProvider },
    );
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.save(async () => {
        await api.put("/api/v1/fake-config", { host: "new.example" }, null);
      });
    });

    expect(result.current.saved).toBe(true);
    expect(result.current.saveError).toBeNull();
    expect(result.current.submitting).toBe(false);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/fake-config", expect.objectContaining({ method: "PUT" }));
    // reload() re-fetches the GET -- called once on mount, once after save.
    expect(fetchMock.mock.calls.filter(([, init]) => !init || init.method === undefined || init.method === "GET").length).toBeGreaterThanOrEqual(2);
  });

  it("save() sets saveError and leaves saved false when the mutation fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ host: "smtp.example" })));

    const { result } = renderHook(
      () => useAdminSingletonConfig<FakeConfig | null>(["fake-config"], (tok) => api.get("/api/v1/fake-config", tok)),
      { wrapper: AuthProvider },
    );
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.save(async () => {
        throw new Error("should be caught");
      });
    });

    expect(result.current.saved).toBe(false);
    expect(result.current.saveError).not.toBeNull();
    expect(result.current.submitting).toBe(false);
  });

  it("save() with markSavedOnSuccess: false still reloads and clears submitting, but never sets saved -- for a delete/remove mutation", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(null));
    });
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(
      () => useAdminSingletonConfig<FakeConfig | null>(["fake-config"], (tok) => api.get("/api/v1/fake-config", tok)),
      { wrapper: AuthProvider },
    );
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.save(
        async () => {
          await api.del("/api/v1/fake-config", null);
        },
        { markSavedOnSuccess: false },
      );
    });

    expect(result.current.saved).toBe(false);
    expect(result.current.submitting).toBe(false);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/fake-config", expect.objectContaining({ method: "DELETE" }));
  });

  it("save() clears a previous saveError/saved before starting a new attempt", async () => {
    let shouldFail = true;
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ host: "smtp.example" })));

    const { result } = renderHook(
      () => useAdminSingletonConfig<FakeConfig | null>(["fake-config"], (tok) => api.get("/api/v1/fake-config", tok)),
      { wrapper: AuthProvider },
    );
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.save(async () => {
        if (shouldFail) throw new Error("first attempt fails");
      });
    });
    expect(result.current.saveError).not.toBeNull();

    shouldFail = false;
    await act(async () => {
      await result.current.save(async () => {
        // second attempt succeeds
      });
    });
    expect(result.current.saveError).toBeNull();
    expect(result.current.saved).toBe(true);
  });
});
