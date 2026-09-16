import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { useEventStream } from "./eventStream";
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
});
