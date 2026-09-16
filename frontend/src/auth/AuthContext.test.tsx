import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { AuthProvider, useAuth, isSessionExpiredError } from "./AuthContext";
import { api, ApiError } from "../api/client";

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return { ...actual, api: { ...actual.api, post: vi.fn() } };
});

function fakeToken(claims: Record<string, unknown>): string {
  const header = btoa(JSON.stringify({ alg: "RS256" }));
  const payload = btoa(JSON.stringify(claims)).replace(/\+/g, "-").replace(/\//g, "_");
  return `${header}.${payload}.fake-signature`;
}

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

describe("AuthContext", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.mocked(api.post).mockReset();
  });

  it("starts unauthenticated with no stored session", () => {
    const { result } = renderHook(() => useAuth(), { wrapper });
    expect(result.current.isAuthenticated).toBe(false);
    expect(result.current.user).toBeNull();
  });

  // A reload has a stored user but no access token -- that one is memory-only
  // by design. The session comes back by exchanging the HttpOnly refresh
  // cookie, so "restoring" is a network call, not a localStorage read.
  it("restores a session by refreshing against the cookie, not from localStorage", async () => {
    localStorage.setItem(
      "kuruops.user",
      JSON.stringify({ id: "1", email: "a@b.com", name: "A", role: "Admin", isAdmin: true, mustChangePassword: false, resourceAccess: ["alerts"] }),
    );
    vi.mocked(api.post).mockResolvedValue({ token: fakeToken({ is_admin: true, resource_access: ["alerts"] }) });

    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.isBootstrapping).toBe(false));

    expect(result.current.isAuthenticated).toBe(true);
    expect(result.current.isAdmin).toBe(true);
    // No refresh token anywhere in the request: it rides along as a cookie
    // this code cannot see.
    expect(api.post).toHaveBeenCalledWith("/auth/refresh", {}, null);
  });

  it("logs out when the refresh cookie is gone or rejected", async () => {
    localStorage.setItem(
      "kuruops.user",
      JSON.stringify({ id: "1", email: "a@b.com", name: "A", role: "Admin", isAdmin: true, mustChangePassword: false, resourceAccess: ["alerts"] }),
    );
    vi.mocked(api.post).mockRejectedValue(new Error("401"));

    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.isBootstrapping).toBe(false));

    expect(result.current.isAuthenticated).toBe(false);
    expect(localStorage.getItem("kuruops.user")).toBeNull();
  });

  // Anyone logged in when this shipped has the old { token, refreshToken,
  // user } blob in storage. Reading the user back out of it means they land
  // on a working session instead of an unexplained logout; the tokens it
  // carries are ignored, which is the entire point of the change.
  it("reads the user out of a pre-cookie stored session, ignoring its tokens", async () => {
    localStorage.setItem(
      "kuruops.user",
      JSON.stringify({ token: "old-tok", refreshToken: "rt_old", user: { id: "1", email: "a@b.com", name: "A", role: "Admin", isAdmin: true, mustChangePassword: false, resourceAccess: ["alerts"] } }),
    );
    vi.mocked(api.post).mockResolvedValue({ token: fakeToken({ is_admin: true, resource_access: ["alerts"] }) });

    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.isBootstrapping).toBe(false));

    expect(result.current.user?.email).toBe("a@b.com");
    expect(result.current.token).not.toBe("old-tok");
  });

  it("ignores corrupt stored session data instead of throwing", () => {
    localStorage.setItem("kuruops.user", "not json");
    const { result } = renderHook(() => useAuth(), { wrapper });
    expect(result.current.isAuthenticated).toBe(false);
  });

  it("loginLocal decodes isAdmin/resourceAccess from the JWT payload, not the response body", async () => {
    const token = fakeToken({ must_change_password: true, is_admin: false, resource_access: ["alerts", "followup"] });
    vi.mocked(api.post).mockResolvedValue({
      token,
      user: { id: "1", email: "analyst@test.local", name: "Analyst", role: "Analyst", mustChangePassword: true },
    });

    const { result } = renderHook(() => useAuth(), { wrapper });
    await act(async () => {
      await result.current.loginLocal("analyst@test.local", "pw");
    });

    expect(result.current.isAuthenticated).toBe(true);
    expect(result.current.isAdmin).toBe(false);
    expect(result.current.user?.resourceAccess).toEqual(["alerts", "followup"]);
    expect(result.current.mustChangePassword).toBe(true);
    expect(result.current.hasResourceAccess("alerts")).toBe(true);
    expect(result.current.hasResourceAccess("incidents")).toBe(false);
  });

  it("loginLocal decodes mfaEnabled from the JWT payload too", async () => {
    const token = fakeToken({ mfa_enabled: true });
    vi.mocked(api.post).mockResolvedValue({
      token,
      user: { id: "1", email: "a@b.com", name: "A", role: "Admin", mustChangePassword: false },
    });
    const { result } = renderHook(() => useAuth(), { wrapper });
    await act(async () => {
      await result.current.loginLocal("a@b.com", "pw");
    });
    expect(result.current.user?.mfaEnabled).toBe(true);
  });

  it("loginLocal returns mfaRequired + the pending token for a TOTP-enrolled account, without starting a session", async () => {
    vi.mocked(api.post).mockResolvedValue({ mfaRequired: true, pendingToken: "mfap_abc123" });
    const { result } = renderHook(() => useAuth(), { wrapper });

    let loginResult: Awaited<ReturnType<typeof result.current.loginLocal>> | undefined;
    await act(async () => {
      loginResult = await result.current.loginLocal("a@b.com", "pw");
    });

    expect(loginResult).toEqual({ mfaRequired: true, pendingToken: "mfap_abc123" });
    expect(result.current.isAuthenticated).toBe(false);
    expect(api.post).toHaveBeenCalledWith("/auth/login", { email: "a@b.com", password: "pw" }, null);
  });

  it("verifyMfa completes the login and starts a session, decoded from the returned token same as loginLocal", async () => {
    const token = fakeToken({ is_admin: true, resource_access: ["alerts"] });
    vi.mocked(api.post).mockResolvedValue({
      token,
      user: { id: "1", email: "a@b.com", name: "A", role: "Admin", mustChangePassword: false },
    });
    const { result } = renderHook(() => useAuth(), { wrapper });

    await act(async () => {
      await result.current.verifyMfa("mfap_abc123", "123456");
    });

    expect(api.post).toHaveBeenCalledWith("/auth/mfa/verify", { pendingToken: "mfap_abc123", code: "123456" }, null);
    expect(result.current.isAuthenticated).toBe(true);
    expect(result.current.isAdmin).toBe(true);
    expect(result.current.user?.resourceAccess).toEqual(["alerts"]);
  });

  it("a malformed token decodes to safe defaults instead of throwing", async () => {
    vi.mocked(api.post).mockResolvedValue({
      token: "not-a-real-jwt",
      user: { id: "1", email: "a@b.com", name: "A", role: "viewer", mustChangePassword: false },
    });
    const { result } = renderHook(() => useAuth(), { wrapper });
    await act(async () => {
      await result.current.loginLocal("a@b.com", "pw");
    });
    expect(result.current.user?.resourceAccess).toEqual([]);
  });

  it("applyNewToken updates mustChangePassword/resourceAccess without touching other user fields", async () => {
    const initialToken = fakeToken({ must_change_password: true, resource_access: [] });
    vi.mocked(api.post).mockResolvedValue({
      token: initialToken,
      user: { id: "1", email: "a@b.com", name: "A", role: "admin", mustChangePassword: true },
    });
    const { result } = renderHook(() => useAuth(), { wrapper });
    await act(async () => {
      await result.current.loginLocal("a@b.com", "pw");
    });

    const freshToken = fakeToken({ must_change_password: false, resource_access: ["alerts", "incidents"] });
    act(() => {
      result.current.applyNewToken(freshToken);
    });

    expect(result.current.mustChangePassword).toBe(false);
    expect(result.current.user?.resourceAccess).toEqual(["alerts", "incidents"]);
    expect(result.current.user?.email).toBe("a@b.com");
  });

  it("applyNewToken is a no-op when there is no logged-in user", () => {
    const { result } = renderHook(() => useAuth(), { wrapper });
    act(() => {
      result.current.applyNewToken(fakeToken({}));
    });
    expect(result.current.user).toBeNull();
  });

  it("logout clears the session and localStorage", async () => {
    vi.mocked(api.post).mockResolvedValue({
      token: fakeToken({}),
      user: { id: "1", email: "a@b.com", name: "A", role: "admin", mustChangePassword: false },
    });
    const { result } = renderHook(() => useAuth(), { wrapper });
    await act(async () => {
      await result.current.loginLocal("a@b.com", "pw");
    });
    expect(result.current.isAuthenticated).toBe(true);

    act(() => {
      result.current.logout();
    });
    expect(result.current.isAuthenticated).toBe(false);
    // The entry is removed outright rather than rewritten with nulls --
    // nothing is left behind to reason about on the next load.
    expect(localStorage.getItem("kuruops.user")).toBeNull();
    // No body: the backend clears the cookie it can see, and this code
    // has no token to send.
    expect(api.post).toHaveBeenCalledWith("/auth/logout", {}, null);
  });

  it("useAuth throws outside of AuthProvider", () => {
    expect(() => renderHook(() => useAuth())).toThrow("useAuth must be used within AuthProvider");
  });
});

describe("isSessionExpiredError", () => {
  it("is true for a 401 ApiError", () => {
    expect(isSessionExpiredError(new ApiError(401, "expired"))).toBe(true);
  });

  it("is false for other ApiError statuses", () => {
    expect(isSessionExpiredError(new ApiError(403, "forbidden"))).toBe(false);
  });

  it("is false for a non-ApiError", () => {
    expect(isSessionExpiredError(new Error("boom"))).toBe(false);
  });
});
