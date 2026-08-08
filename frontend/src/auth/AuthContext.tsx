import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, ApiError, setRefreshHandler } from "../api/client";
import type { LoginResponse } from "../types/api";

interface SessionUser {
  id: string;
  email: string;
  name: string;
  // Display-only: the assigned Role's name (e.g. "Admin", "Analyst 1").
  // Never used for an authorization decision -- see isAdmin/resourceAccess.
  role: string;
  mustChangePassword: boolean;
  // isAdmin and resourceAccess are decoded from the JWT (not the login
  // response body) so they work uniformly across local/LDAP/SAML -- SAML's
  // redirect flow has no JSON body for the frontend to read them out of,
  // only the token itself. See decodeTokenClaims.
  isAdmin: boolean;
  resourceAccess: string[];
}

interface AuthState {
  token: string | null;
  refreshToken: string | null;
  user: SessionUser | null;
}

interface AuthContextValue extends AuthState {
  isAuthenticated: boolean;
  isAdmin: boolean;
  mustChangePassword: boolean;
  // Convenience helpers over user.resourceAccess -- see domain.ResourceCapability*
  // on the backend. hasResourceAccess("followup") etc.
  hasResourceAccess: (capability: string) => boolean;
  loginLocal: (email: string, password: string) => Promise<void>;
  // Called after POST /account/change-password succeeds -- swaps in the
  // freshly re-issued token (mustChangePassword cleared) without forcing a
  // new login.
  applyNewToken: (token: string) => void;
  // Called after PUT /account/profile succeeds -- name/email aren't part of
  // the JWT claims (see decodeTokenClaims), so this patches the locally
  // held user directly from the response body instead of decoding a token.
  updateProfile: (name: string, email: string) => void;
  logout: () => void;
}

const STORAGE_KEY = "argusops.session";

function loadStoredSession(): AuthState {
  const raw = localStorage.getItem(STORAGE_KEY);
  if (!raw) return { token: null, refreshToken: null, user: null };
  try {
    return JSON.parse(raw) as AuthState;
  } catch {
    return { token: null, refreshToken: null, user: null };
  }
}

// decodeTokenClaims reads must_change_password/resource_access straight out
// of the JWT payload (base64url, no signature check needed client-side --
// the backend re-verifies on every request) so callers don't need the login
// response shape, just the token string -- the one thing every login path
// (local, LDAP, SAML) actually produces.
function decodeTokenClaims(token: string): { mustChangePassword: boolean; isAdmin: boolean; resourceAccess: string[] } {
  try {
    const payload = token.split(".")[1];
    const json = atob(payload.replace(/-/g, "+").replace(/_/g, "/"));
    const claims = JSON.parse(json);
    return {
      mustChangePassword: Boolean(claims.must_change_password),
      isAdmin: Boolean(claims.is_admin),
      resourceAccess: Array.isArray(claims.resource_access) ? claims.resource_access : [],
    };
  } catch {
    return { mustChangePassword: false, isAdmin: false, resourceAccess: [] };
  }
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>(loadStoredSession);
  // refreshAccessToken (below) is registered once with api/client.ts as a
  // module-level callback -- it can't close over `state` directly (it would
  // capture whatever refreshToken existed at registration time), so it
  // reads through this ref instead.
  const stateRef = useRef(state);
  useEffect(() => {
    stateRef.current = state;
  }, [state]);

  const persist = useCallback((next: AuthState) => {
    setState(next);
    localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  }, []);

  const loginLocal = useCallback(
    async (email: string, password: string) => {
      const res = await api.post<LoginResponse>("/auth/login", { email, password }, null);
      const { isAdmin, resourceAccess } = decodeTokenClaims(res.token);
      persist({ token: res.token, refreshToken: res.refreshToken, user: { ...res.user, isAdmin, resourceAccess } });
    },
    [persist],
  );

  const applyNewToken = useCallback(
    (token: string) => {
      setState((prev) => {
        if (!prev.user) return prev;
        const claims = decodeTokenClaims(token);
        const next: AuthState = {
          ...prev,
          token,
          user: { ...prev.user, mustChangePassword: claims.mustChangePassword, isAdmin: claims.isAdmin, resourceAccess: claims.resourceAccess },
        };
        localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
        return next;
      });
    },
    [],
  );

  const updateProfile = useCallback(
    (name: string, email: string) => {
      setState((prev) => {
        if (!prev.user) return prev;
        const next: AuthState = { ...prev, user: { ...prev.user, name, email } };
        localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
        return next;
      });
    },
    [],
  );

  const logout = useCallback(() => {
    persist({ token: null, refreshToken: null, user: null });
  }, [persist]);

  // refreshAccessToken exchanges the stored refresh token for a new access
  // token via POST /auth/refresh, rotating the refresh token itself (see
  // AuthService.Refresh) -- registered below as api/client.ts's
  // refreshHandler, so a 401 from any authenticated call is retried once
  // transparently instead of immediately forcing a re-login. Returns null
  // (and logs out) when there's no refresh token to use or the backend
  // rejects it (expired/revoked -- e.g. the account was deactivated).
  const refreshAccessToken = useCallback(async (): Promise<string | null> => {
    const current = stateRef.current;
    if (!current.refreshToken || !current.user) return null;
    try {
      const res = await api.post<{ token: string; refreshToken: string }>(
        "/auth/refresh",
        { refreshToken: current.refreshToken },
        null,
      );
      const claims = decodeTokenClaims(res.token);
      persist({
        token: res.token,
        refreshToken: res.refreshToken,
        user: { ...current.user, mustChangePassword: claims.mustChangePassword, isAdmin: claims.isAdmin, resourceAccess: claims.resourceAccess },
      });
      return res.token;
    } catch {
      persist({ token: null, refreshToken: null, user: null });
      return null;
    }
  }, [persist]);

  useEffect(() => {
    setRefreshHandler(refreshAccessToken);
    return () => setRefreshHandler(null);
  }, [refreshAccessToken]);

  const value = useMemo<AuthContextValue>(
    () => ({
      ...state,
      isAuthenticated: state.token !== null,
      isAdmin: state.user?.isAdmin ?? false,
      mustChangePassword: state.user?.mustChangePassword ?? false,
      hasResourceAccess: (capability) => state.user?.resourceAccess.includes(capability) ?? false,
      loginLocal,
      applyNewToken,
      updateProfile,
      logout,
    }),
    [state, loginLocal, applyNewToken, updateProfile, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}

// isSessionExpiredError lets a panel tell "your session expired" apart from
// "you don't have permission" -- both surface as ApiError but call for
// different UI (redirect to login vs. an inline forbidden message).
export function isSessionExpiredError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401;
}
