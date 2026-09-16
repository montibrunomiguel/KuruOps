// Test helpers for rendering a component as an already-logged-in user.
//
// This exists because the access token is deliberately never persisted:
// it lives in React state only, and a page load recovers a session by
// exchanging the HttpOnly refresh cookie at /auth/refresh (see
// AuthProvider's bootstrapping effect). A test that renders <AuthProvider>
// is therefore doing exactly what a browser reload does, and needs the same
// two things a reload needs -- the stored display user, and a refresh call
// that answers with a token.
//
// Dropping a token into localStorage would be simpler and would also be
// testing a code path that no longer exists.

const USER_STORAGE_KEY = "kuruops.user";

export interface SeedSessionOptions {
  id?: string;
  email?: string;
  name?: string;
  phone?: string;
  role?: string;
  isAdmin?: boolean;
  mustChangePassword?: boolean;
  mfaEnabled?: boolean;
  resourceAccess?: string[];
}

// The claims AuthContext.decodeTokenClaims reads back out of the token.
// They deliberately override whatever the stored user says, because in
// production the JWT is the only authority on them -- so a test that seeds
// isAdmin must have it end up in the minted token too, not just in
// localStorage.
function mintToken(
  opts: Required<
    Pick<
      SeedSessionOptions,
      "isAdmin" | "mustChangePassword" | "mfaEnabled" | "resourceAccess"
    >
  >,
): string {
  const payload = {
    is_admin: opts.isAdmin,
    must_change_password: opts.mustChangePassword,
    mfa_enabled: opts.mfaEnabled,
    resource_access: opts.resourceAccess,
  };
  // Header and signature are inert: decodeTokenClaims only base64-decodes
  // the middle segment and never verifies anything (the backend does that).
  return `header.${btoa(JSON.stringify(payload))}.signature`;
}

let currentToken = "";

// seedSession writes the stored half of a session and remembers the token
// that withSession should hand back from /auth/refresh. Call it before
// rendering, and wrap the test's fetch mock in withSession.
export function seedSession(opts: SeedSessionOptions = {}): string {
  const resolved = {
    isAdmin: opts.isAdmin ?? false,
    mustChangePassword: opts.mustChangePassword ?? false,
    mfaEnabled: opts.mfaEnabled ?? false,
    resourceAccess: opts.resourceAccess ?? ["alerts", "incidents", "followup"],
  };
  localStorage.setItem(
    USER_STORAGE_KEY,
    JSON.stringify({
      id: opts.id ?? "1",
      email: opts.email ?? "user@test.local",
      name: opts.name ?? "User",
      phone: opts.phone,
      role: opts.role ?? "Analyst",
      ...resolved,
    }),
  );
  currentToken = mintToken(resolved);
  return currentToken;
}

// withSession answers AuthProvider's bootstrap call and delegates
// everything else to the test's own mock, so per-test fetch expectations
// stay exactly as they were.
//
// It returns a Proxy rather than a wrapper function for two reasons, both
// of which a plain wrapper gets wrong. Property reads pass straight through
// to the underlying mock, so `expect(fetch).toHaveBeenCalledWith(...)` and
// `fetch.mock.calls` keep working on what is still a real spy. And the
// bootstrap refresh is intercepted in the `apply` trap, so it never reaches
// the spy at all -- which is what lets a test go on asserting
// `expect(fetch).not.toHaveBeenCalled()` to mean "the component under test
// made no request", without AuthProvider's own session call counting
// against it.
export function withSession(inner: typeof fetch): typeof fetch {
  return new Proxy(inner, {
    apply(target, thisArg, args: [RequestInfo | URL, RequestInit?]) {
      const [input, init] = args;
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.toString()
            : input.url;
      if (url.includes("/auth/refresh")) {
        return Promise.resolve(
          new Response(JSON.stringify({ token: currentToken }), {
            status: 200,
            headers: { "content-type": "application/json" },
          }),
        );
      }
      return Reflect.apply(target, thisArg, [input, init]);
    },
  });
}
