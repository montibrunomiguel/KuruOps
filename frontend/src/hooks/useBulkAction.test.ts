import { describe, it, expect, vi } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { useBulkAction } from "./useBulkAction";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("useBulkAction", () => {
  it("starts idle: not applying, no error, no summary", () => {
    vi.stubGlobal("fetch", vi.fn());
    const { result } = renderHook(() => useBulkAction("/api/v1/alerts/bulk/status", "tok"));
    expect(result.current.applying).toBe(false);
    expect(result.current.error).toBeNull();
    expect(result.current.summary).toBeNull();
  });

  it("posts ids + extra fields to the endpoint, tallies the response, and calls onSuccess", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ results: [{ id: "a", success: true }, { id: "b", success: true }, { id: "c", success: false }] }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => useBulkAction("/api/v1/alerts/bulk/status", "tok"));
    const onSuccess = vi.fn();

    await act(async () => {
      await result.current.apply(["a", "b", "c"], { status: "investigating" }, onSuccess);
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/alerts/bulk/status",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ ids: ["a", "b", "c"], status: "investigating" }),
      }),
    );
    expect(result.current.summary).toEqual({ success: 2, failed: 1 });
    expect(result.current.applying).toBe(false);
    expect(onSuccess).toHaveBeenCalledOnce();
  });

  it("sets applying while the request is in flight", async () => {
    let resolveRequest!: (r: Response) => void;
    const fetchMock = vi.fn().mockReturnValue(new Promise<Response>((resolve) => (resolveRequest = resolve)));
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => useBulkAction("/api/v1/incidents/bulk/phase", "tok"));

    let applyPromise!: Promise<void>;
    act(() => {
      applyPromise = result.current.apply(["a"], { phase: "containment" }, vi.fn());
    });
    expect(result.current.applying).toBe(true);

    await act(async () => {
      resolveRequest(jsonResponse({ results: [{ id: "a", success: true }] }));
      await applyPromise;
    });
    expect(result.current.applying).toBe(false);
  });

  it("on failure, sets error, leaves summary null, and does not call onSuccess", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "server exploded" }, 500)));
    const { result } = renderHook(() => useBulkAction("/api/v1/alerts/bulk/status", "tok"));
    const onSuccess = vi.fn();

    await act(async () => {
      await result.current.apply(["a"], { status: "open" }, onSuccess);
    });

    await waitFor(() => expect(result.current.error).not.toBeNull());
    expect(result.current.summary).toBeNull();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it("a later successful apply clears a previous error", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "boom" }, 500))
      .mockResolvedValueOnce(jsonResponse({ results: [{ id: "a", success: true }] }));
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => useBulkAction("/api/v1/alerts/bulk/status", "tok"));

    await act(async () => {
      await result.current.apply(["a"], { status: "open" }, vi.fn());
    });
    expect(result.current.error).not.toBeNull();

    await act(async () => {
      await result.current.apply(["a"], { status: "open" }, vi.fn());
    });
    expect(result.current.error).toBeNull();
    expect(result.current.summary).toEqual({ success: 1, failed: 0 });
  });
});
