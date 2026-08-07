import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
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

  it("restores a previously stored session from localStorage", () => {
    localStorage.setItem(
      "argusops.session",
      JSON.stringify({ token: "tok", user: { id: "1", email: "a@b.com", name: "A", role: "admin", mustChangePassword: false, resourceAccess: ["alerts"] } }),
    );
    const { result } = renderHook(() => useAuth(), { wrapper });
    expect(result.current.isAuthenticated).toBe(true);
    expect(result.current.isAdmin).toBe(true);
  });

  it("ignores corrupt stored session data instead of throwing", () => {
    localStorage.setItem("argusops.session", "not json");
    const { result } = renderHook(() => useAuth(), { wrapper });
    expect(result.current.isAuthenticated).toBe(false);
  });

  it("loginLocal decodes resourceAccess from the JWT payload, not the response body", async () => {
    const token = fakeToken({ must_change_password: true, resource_access: ["alerts", "followup"] });
    vi.mocked(api.post).mockResolvedValue({
      token,
      refreshToken: "rt_1",
      user: { id: "1", email: "analyst@test.local", name: "Analyst", role: "analyst", mustChangePassword: true },
    });

    const { result } = renderHook(() => useAuth(), { wrapper });
    await act(async () => {
      await result.current.loginLocal("analyst@test.local", "pw");
    });

    expect(result.current.isAuthenticated).toBe(true);
    expect(result.current.user?.resourceAccess).toEqual(["alerts", "followup"]);
    expect(result.current.mustChangePassword).toBe(true);
    expect(result.current.hasResourceAccess("alerts")).toBe(true);
    expect(result.current.hasResourceAccess("incidents")).toBe(false);
  });

  it("a malformed token decodes to safe defaults instead of throwing", async () => {
    vi.mocked(api.post).mockResolvedValue({
      token: "not-a-real-jwt",
      refreshToken: "rt_1",
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
      refreshToken: "rt_1",
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
      refreshToken: "rt_1",
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
    expect(localStorage.getItem("argusops.session")).toContain('"token":null');
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
