import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { api, ApiError, setRefreshHandler } from "./client";

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

describe("api client", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn());
  });

  it("GET sends no body and an Authorization header when a token is given", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(200, { ok: true }));

    const result = await api.get<{ ok: boolean }>("/api/v1/alerts", "tok123");

    expect(result).toEqual({ ok: true });
    const [url, init] = vi.mocked(fetch).mock.calls[0];
    expect(url).toBe("/api/v1/alerts");
    expect(init?.method).toBe("GET");
    expect(init?.body).toBeUndefined();
    expect((init?.headers as Record<string, string>).Authorization).toBe("Bearer tok123");
  });

  it("omits the Authorization header when token is null", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(200, {}));
    await api.get("/api/v1/tags", null);
    const [, init] = vi.mocked(fetch).mock.calls[0];
    expect((init?.headers as Record<string, string>).Authorization).toBeUndefined();
  });

  it("POST sends a JSON body and Content-Type header", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(201, { id: "1" }));
    await api.post("/api/v1/tags", { name: "phishing" }, "tok");

    const [, init] = vi.mocked(fetch).mock.calls[0];
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe(JSON.stringify({ name: "phishing" }));
    expect((init?.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });

  it("PUT and DELETE use the right HTTP methods", async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 204 }));
    await api.put("/api/v1/tags/1", { name: "x" }, "tok");
    expect(vi.mocked(fetch).mock.calls[0][1]?.method).toBe("PUT");

    await api.del("/api/v1/tags/1", "tok");
    expect(vi.mocked(fetch).mock.calls[1][1]?.method).toBe("DELETE");
  });

  it("a 204 response resolves to undefined without attempting to parse a body", async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 204 }));
    const result = await api.del("/api/v1/tags/1", "tok");
    expect(result).toBeUndefined();
  });

  it("a non-JSON success response resolves to undefined", async () => {
    vi.mocked(fetch).mockResolvedValue(new Response("plain text", { status: 200 }));
    const result = await api.get("/api/v1/x", "tok");
    expect(result).toBeUndefined();
  });

  it("an error response with a JSON error field throws ApiError with that message", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(400, { error: "tag name is required" }));
    await expect(api.post("/api/v1/tags", {}, "tok")).rejects.toMatchObject({
      name: "ApiError",
      status: 400,
      message: "tag name is required",
    });
  });

  it("an error response without a JSON body falls back to statusText", async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 500, statusText: "Internal Server Error" }));
    await expect(api.get("/api/v1/x", "tok")).rejects.toThrow("Internal Server Error");
  });

  it("ApiError is an instance of Error and carries the status", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(404, { error: "not found" }));
    try {
      await api.get("/api/v1/alerts/1", "tok");
      expect.unreachable();
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError);
      expect(err).toBeInstanceOf(Error);
      expect((err as ApiError).status).toBe(404);
    }
  });
});

describe("api client -- 401 refresh-and-retry", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn());
  });

  afterEach(() => {
    setRefreshHandler(null);
  });

  it("a 401 on an authenticated request is retried once with a refreshed token", async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(jsonResponse(401, { error: "expired" }))
      .mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    const refreshHandler = vi.fn().mockResolvedValue("new-token");
    setRefreshHandler(refreshHandler);

    const result = await api.get<{ ok: boolean }>("/api/v1/alerts", "old-token");

    expect(result).toEqual({ ok: true });
    expect(refreshHandler).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledTimes(2);
    const secondCallHeaders = vi.mocked(fetch).mock.calls[1][1]?.headers as Record<string, string>;
    expect(secondCallHeaders.Authorization).toBe("Bearer new-token");
  });

  it("throws the original 401 when the refresh handler can't produce a new token", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(401, { error: "expired" }));
    setRefreshHandler(vi.fn().mockResolvedValue(null));

    await expect(api.get("/api/v1/alerts", "old-token")).rejects.toMatchObject({ status: 401 });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("a 401 with no token (e.g. login itself) is never retried", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(401, { error: "invalid credentials" }));
    const refreshHandler = vi.fn();
    setRefreshHandler(refreshHandler);

    await expect(api.post("/auth/login", { email: "a@b.com", password: "x" }, null)).rejects.toMatchObject({
      status: 401,
    });
    expect(refreshHandler).not.toHaveBeenCalled();
  });

  it("a retried request that also gets a 401 does not loop again", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(401, { error: "still expired" }));
    const refreshHandler = vi.fn().mockResolvedValue("new-token");
    setRefreshHandler(refreshHandler);

    await expect(api.get("/api/v1/alerts", "old-token")).rejects.toMatchObject({ status: 401 });
    expect(refreshHandler).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("concurrent 401s share a single in-flight refresh call", async () => {
    let resolveRefresh!: (token: string) => void;
    const refreshHandler = vi.fn().mockReturnValue(
      new Promise<string>((resolve) => {
        resolveRefresh = resolve;
      }),
    );
    setRefreshHandler(refreshHandler);
    vi.mocked(fetch).mockImplementation((_url, init) => {
      const headers = init?.headers as Record<string, string> | undefined;
      if (headers?.Authorization === "Bearer new-token") {
        return Promise.resolve(jsonResponse(200, { ok: true }));
      }
      return Promise.resolve(jsonResponse(401, { error: "expired" }));
    });

    const p1 = api.get("/api/v1/alerts", "old-token");
    const p2 = api.get("/api/v1/incidents", "old-token");
    // Let both initial 401s land and call refreshOnce before resolving it.
    await Promise.resolve();
    await Promise.resolve();
    resolveRefresh("new-token");

    await Promise.all([p1, p2]);
    expect(refreshHandler).toHaveBeenCalledTimes(1);
  });

describe("gateway failures", () => {
  // 502/503/504 come from a proxy, not the API, so they carry no {error}
  // body. The old fallback rendered the raw status line, which is how a
  // login form ended up telling someone "Bad Gateway".
  it.each([502, 503, 504])("maps a %i into something a person can act on", async (status) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("", { status, statusText: "Bad Gateway" })));
    await expect(api.get("/api/v1/alerts", "tok")).rejects.toMatchObject({
      status,
      message: expect.stringMatching(/unreachable|indispon/i),
    });
  });

  it("still prefers the backend's own message when there is one", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "alert not found" }), {
          status: 404,
          headers: { "content-type": "application/json" },
        }),
      ),
    );
    await expect(api.get("/api/v1/alerts/x", "tok")).rejects.toMatchObject({ message: "alert not found" });
  });
});

});
