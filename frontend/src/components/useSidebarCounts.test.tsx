import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { useSidebarCounts } from "./useSidebarCounts";
import { AuthProvider } from "../auth/AuthContext";

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

function loggedInSession() {
  localStorage.setItem(
    "kuruops.session",
    JSON.stringify({
      token: "tok",
      user: { id: "1", email: "a@b.com", name: "A", role: "analyst", mustChangePassword: false, resourceAccess: ["alerts", "incidents"] },
    }),
  );
}

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

describe("useSidebarCounts", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("does not fetch either count when not authenticated", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    renderHook(() => useSidebarCounts(true, true), { wrapper });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("skips the alerts fetch when canAlerts is false, still fetches incidents", async () => {
    loggedInSession();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);

    renderHook(() => useSidebarCounts(false, true), { wrapper });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(fetchMock.mock.calls[0][0]).toContain("/api/v1/incidents");
  });

  it("counts open alerts and non-post_incident incidents", async () => {
    loggedInSession();
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/alerts")) return Promise.resolve(jsonResponse([{ id: "1" }, { id: "2" }]));
      return Promise.resolve(
        jsonResponse([{ id: "1", phase: "new" }, { id: "2", phase: "post_incident" }, { id: "3", phase: "containment" }]),
      );
    });
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useSidebarCounts(true, true), { wrapper });
    await waitFor(() => expect(result.current.openAlerts).toBe(2));
    expect(result.current.activeIncidents).toBe(2);
  });

  it("a failed fetch resolves the count to null rather than throwing", async () => {
    loggedInSession();
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));

    const { result } = renderHook(() => useSidebarCounts(true, false), { wrapper });
    await waitFor(() => expect(result.current.openAlerts).toBeNull());
  });
});
