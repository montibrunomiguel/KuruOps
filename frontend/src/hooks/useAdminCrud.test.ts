import { describe, it, expect, vi } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { AuthProvider } from "../auth/AuthContext";
import { useAdminCrud } from "./useAdminCrud";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("useAdminCrud", () => {
  it("fetches the list from basePath by default", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([{ id: "t1" }]));
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useAdminCrud<{ id: string }>("/api/v1/settings/things"), {
      wrapper: AuthProvider,
    });

    await waitFor(() => expect(result.current.data).toEqual([{ id: "t1" }]));
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/things", expect.anything());
  });

  it("fetches from a separate listPath when given, not basePath", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);

    renderHook(() => useAdminCrud<{ id: string }>("/api/v1/settings/tags", "/api/v1/tags"), {
      wrapper: AuthProvider,
    });

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/tags", expect.anything()),
    );
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/tags", expect.anything());
  });

  it("remove() DELETEs at basePath/id, clears confirming, and reloads", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useAdminCrud<{ id: string }>("/api/v1/settings/things"), {
      wrapper: AuthProvider,
    });
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.confirm("row-1"));
    expect(result.current.confirming).toBe("row-1");

    await act(async () => {
      await result.current.remove("row-1");
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/settings/things/row-1",
      expect.objectContaining({ method: "DELETE" }),
    );
    expect(result.current.confirming).toBeNull();
  });

  it("remove() sets deleteError and leaves confirming intact on failure", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(jsonResponse({ error: "cannot delete" }, 400));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useAdminCrud<{ id: string }>("/api/v1/settings/things"), {
      wrapper: AuthProvider,
    });
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.confirm("row-1"));
    await act(async () => {
      await result.current.remove("row-1");
    });

    expect(result.current.deleteError).toBe("cannot delete");
    expect(result.current.confirming).toBe("row-1");
  });
});
