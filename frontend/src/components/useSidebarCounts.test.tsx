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

// A never-resolving SSE stream -- useEventStream's own connection, wired up
// whenever the caller is authenticated (this hook now subscribes to live
// updates via the same useEventStream every list/detail page uses -- see
// its own doc comment). Kept idle for these tests, which only care about
// the two GET-list requests; AlertsListPage.test.tsx already covers a real
// SSE frame triggering a reload.
function idleStream() {
  return new Response(new ReadableStream({ start() {} }), { status: 200 });
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
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.startsWith("/api/v1/events/stream")) return Promise.resolve(idleStream());
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);

    renderHook(() => useSidebarCounts(false, true), { wrapper });
    await waitFor(() => expect(fetchMock.mock.calls.some((c) => (c[0] as string).includes("/api/v1/incidents"))).toBe(true));
    expect(fetchMock.mock.calls.some((c) => (c[0] as string).includes("/api/v1/alerts"))).toBe(false);
  });

  it("counts open alerts and non-post_incident incidents", async () => {
    loggedInSession();
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.startsWith("/api/v1/events/stream")) return Promise.resolve(idleStream());
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

  it("reloads the open-alerts count when an SSE alert event arrives", async () => {
    loggedInSession();
    const encoder = new TextEncoder();
    const streamResponse = new Response(
      new ReadableStream({
        start(controller) {
          // Deferred so the frame lands after the initial GETs resolve --
          // same reasoning as AlertsListPage.test.tsx's identical SSE test.
          setTimeout(() => {
            controller.enqueue(encoder.encode('event: alert\ndata: {"id":"a1"}\n\n'));
            controller.close();
          }, 0);
        },
      }),
      { status: 200 },
    );
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.startsWith("/api/v1/events/stream")) return Promise.resolve(streamResponse);
      if (url.includes("/api/v1/alerts")) return Promise.resolve(jsonResponse([{ id: "1" }]));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);

    renderHook(() => useSidebarCounts(true, true), { wrapper });

    await waitFor(() => {
      const alertsCalls = fetchMock.mock.calls.filter((c) => (c[0] as string).includes("/api/v1/alerts")).length;
      expect(alertsCalls).toBeGreaterThanOrEqual(2);
    });
  });
});
