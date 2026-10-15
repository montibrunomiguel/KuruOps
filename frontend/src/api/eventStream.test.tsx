import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { useEventStream, reconnectDelay } from "./eventStream";
import { AuthProvider } from "../auth/AuthContext";
import { seedSession, withSession } from "../test/session";

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

function sessionWith() {
  return seedSession({ id: "1", email: "a@b.com", name: "A", role: "admin", mustChangePassword: false, resourceAccess: [] });
}

function streamOf(...chunks: string[]) {
  const encoder = new TextEncoder();
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
}

describe("useEventStream", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("connects with the bearer token and parses an SSE frame", async () => {
    const token = sessionWith();
    const fetchMock = vi.fn().mockResolvedValue(new Response(streamOf('event: alert\ndata: {"id":"a1","action":"received"}\n\n'), { status: 200 }));
    vi.stubGlobal("fetch", withSession(fetchMock));

    const onEvent = vi.fn();
    renderHook(() => useEventStream(onEvent), { wrapper });

    await waitFor(() => expect(onEvent).toHaveBeenCalledWith({ type: "alert", data: { id: "a1", action: "received" } }));
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/events/stream",
      expect.objectContaining({ headers: { Authorization: `Bearer ${token}` } }),
    );
  });

  it("ignores keep-alive ping comments and parses subsequent real events", async () => {
    sessionWith();
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(new Response(streamOf(": ping\n\n", 'event: incident\ndata: {"id":"i1"}\n\n'), { status: 200 }))));

    const onEvent = vi.fn();
    renderHook(() => useEventStream(onEvent), { wrapper });

    await waitFor(() => expect(onEvent).toHaveBeenCalledTimes(1));
    expect(onEvent).toHaveBeenCalledWith({ type: "incident", data: { id: "i1" } });
  });

  it("handles a frame split across two stream chunks", async () => {
    sessionWith();
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(new Response(streamOf('event: alert\ndata: {"id":"a', '1"}\n\n'), { status: 200 }))));

    const onEvent = vi.fn();
    renderHook(() => useEventStream(onEvent), { wrapper });

    await waitFor(() => expect(onEvent).toHaveBeenCalledWith({ type: "alert", data: { id: "a1" } }));
  });

  it("does not connect when there is no authenticated session", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", withSession(fetchMock));

    renderHook(() => useEventStream(vi.fn()), { wrapper });

    expect(fetchMock).not.toHaveBeenCalled();
  });

  describe("reconnect backoff", () => {
    afterEach(() => {
      vi.restoreAllMocks();
    });

    it("keeps backing off when each failed attempt is slow to fail", async () => {
      // A proxy that takes 11s to answer 502 must not look like a healthy connection
      // (it did, once: the attempt's total duration was used, so the backoff reset).
      sessionWith();
      vi.stubGlobal("fetch", withSession(vi.fn().mockImplementation(() => new Promise((resolve) => setTimeout(() => resolve(new Response("", { status: 502 })), 0)))));
      vi.spyOn(Math, "random").mockReturnValue(1);
      let fakeNow = 0;
      vi.spyOn(Date, "now").mockImplementation(() => (fakeNow += 11000));

      const realSetTimeout = globalThis.setTimeout;
      const delays: number[] = [];
      vi.spyOn(globalThis, "setTimeout").mockImplementation(((fn: () => void, ms?: number, ...rest: unknown[]) => {
        if ((ms ?? 0) >= 1000 && (ms ?? 0) <= 30000) {
          delays.push(ms as number);
          return realSetTimeout(fn, 0);
        }
        return realSetTimeout(fn, ms, ...rest);
      }) as typeof setTimeout);

      renderHook(() => useEventStream(vi.fn()), { wrapper });

      await waitFor(() => expect(delays.length).toBeGreaterThanOrEqual(4), { timeout: 60000 });
      expect(delays.slice(0, 4)).toEqual([1000, 2000, 4000, 8000]);
    });

    it("retries a failing connection with growing, capped delays instead of a fixed one", async () => {
      sessionWith();
      const fetchMock = vi.fn().mockRejectedValue(new Error("down"));
      vi.stubGlobal("fetch", withSession(fetchMock));
      vi.spyOn(Math, "random").mockReturnValue(1); // top of the jitter range: exactly 1s, 2s, 4s...

      // Record the reconnect timers and fire them at once, so the test checks
      // what was scheduled without waiting it out. Reconnect delays are 1s-30s;
      // AuthProvider's own token-refresh timer is far longer and left alone.
      const realSetTimeout = globalThis.setTimeout;
      const delays: number[] = [];
      vi.spyOn(globalThis, "setTimeout").mockImplementation(((fn: () => void, ms?: number, ...rest: unknown[]) => {
        if ((ms ?? 0) >= 1000 && (ms ?? 0) <= 30000) {
          delays.push(ms as number);
          return realSetTimeout(fn, 0);
        }
        return realSetTimeout(fn, ms, ...rest);
      }) as typeof setTimeout);

      renderHook(() => useEventStream(vi.fn()), { wrapper });

      // waitFor's own timeout timer must stay outside the 1s-30s window recorded above.
      await waitFor(() => expect(delays.length).toBeGreaterThanOrEqual(5), { timeout: 60000 });
      expect(delays.slice(0, 5)).toEqual([1000, 2000, 4000, 8000, 16000]);
    });
  });
});

describe("reconnectDelay", () => {
  it("doubles from one second and caps at thirty", () => {
    const top = () => 1;
    expect([0, 1, 2, 3, 4, 5, 6, 20].map((n) => reconnectDelay(n, top))).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000]);
  });

  it("spreads the delay over its upper half so tabs do not reconnect in lockstep", () => {
    expect(reconnectDelay(3, () => 0)).toBe(4000);
    expect(reconnectDelay(3, () => 0.5)).toBe(6000);
    expect(reconnectDelay(3, () => 1)).toBe(8000);
  });

  it("treats a negative failure count as the first attempt", () => {
    expect(reconnectDelay(-5, () => 1)).toBe(1000);
  });
});
