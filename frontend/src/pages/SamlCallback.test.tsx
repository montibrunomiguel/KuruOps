import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { SamlCallbackPage } from "./SamlCallback";
import { AuthProvider } from "../auth/AuthContext";

// A JWT whose payload decodes to the claims AuthContext reads back out.
function fakeToken(claims: Record<string, unknown>): string {
  return `header.${btoa(JSON.stringify(claims))}.sig`;
}

function renderCallback() {
  return render(
    <MemoryRouter initialEntries={["/login/saml"]}>
      <AuthProvider>
        <Routes>
          <Route path="/login/saml" element={<SamlCallbackPage />} />
          <Route path="/dashboard" element={<div>DASHBOARD</div>} />
          <Route path="/login" element={<div>LOGIN</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

function refreshResponds(body: unknown, status = 200) {
  const fetchMock = vi.fn().mockResolvedValue(
    new Response(JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json" },
    }),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

// This route is what closes the gap the backend README used to list: the ACS
// endpoint no longer answers the IdP's POST with a session token, it sets the
// HttpOnly refresh cookie and redirects here. So the assertions that matter
// are that a session appears out of nothing but that cookie, and that no
// credential is ever taken from the URL.
describe("SamlCallbackPage", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  it("trades the refresh cookie for a session and lands on the dashboard", async () => {
    const fetchMock = refreshResponds({
      token: fakeToken({ is_admin: true, resource_access: ["alerts"], must_change_password: false }),
      user: { id: "1", email: "fed@corp.example", name: "Fed User", role: "Admin", mustChangePassword: false },
    });

    renderCallback();

    expect(await screen.findByText("DASHBOARD")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/refresh",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("stores the identity from the response, not from the URL", async () => {
    // There is nothing in the URL to read -- that is the point. The user has
    // to come back on the response body.
    refreshResponds({
      token: fakeToken({ is_admin: false, resource_access: ["incidents"] }),
      user: { id: "7", email: "analyst@corp.example", name: "Analyst", role: "Analyst", mustChangePassword: false },
    });

    renderCallback();

    await waitFor(() => {
      const stored = localStorage.getItem("kuruops.user");
      expect(stored).toContain("analyst@corp.example");
    });
    // Claims win over anything the body says about access.
    expect(localStorage.getItem("kuruops.user")).toContain("incidents");
  });

  it("asks the visitor to sign in again when there is no usable cookie", async () => {
    // What a direct visit to this route looks like. Nothing for the person to
    // fix, so it must not read as a crash.
    refreshResponds({ error: "no refresh token" }, 401);

    renderCallback();

    expect(await screen.findByText(/sign in again/i)).toBeInTheDocument();
    expect(screen.queryByText("DASHBOARD")).not.toBeInTheDocument();
  });

  it("spends the rotating cookie exactly once", async () => {
    // The refresh token rotates on use, so a second call would present a
    // token the first has already consumed -- which StrictMode's double
    // effect invocation would otherwise cause.
    const fetchMock = refreshResponds({
      token: fakeToken({}),
      user: { id: "1", email: "a@b.com", name: "A", role: "Admin", mustChangePassword: false },
    });

    renderCallback();

    await screen.findByText("DASHBOARD");
    const refreshCalls = fetchMock.mock.calls.filter((c) => String(c[0]).includes("/auth/refresh"));
    expect(refreshCalls).toHaveLength(1);
  });
});
