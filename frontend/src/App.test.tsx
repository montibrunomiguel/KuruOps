import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import App from "./App";
import { AuthProvider } from "./auth/AuthContext";

function sessionWith(opts: { resourceAccess?: string[]; role?: string; mustChangePassword?: boolean } = {}) {
  localStorage.setItem(
    "argusops.session",
    JSON.stringify({
      token: "tok",
      user: {
        id: "1",
        email: "user@test.local",
        name: "User",
        role: opts.role ?? "analyst",
        mustChangePassword: opts.mustChangePassword ?? false,
        resourceAccess: opts.resourceAccess ?? ["alerts", "incidents", "followup"],
      },
    }),
  );
}

function renderApp(initialPath: string) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <AuthProvider>
        <App />
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("App routing guards", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200, headers: { "content-type": "application/json" } })));
  });

  it("redirects an unauthenticated visitor from a protected route to /login", async () => {
    renderApp("/alerts");
    expect(await screen.findByLabelText("E-mail")).toBeInTheDocument();
  });

  it("redirects an unknown path to /dashboard, which then redirects to /login when unauthenticated", async () => {
    renderApp("/some-nonexistent-path");
    expect(await screen.findByLabelText("E-mail")).toBeInTheDocument();
  });

  it("redirects to /change-password when the session has mustChangePassword set, for any protected route", async () => {
    sessionWith({ mustChangePassword: true });
    renderApp("/alerts");
    expect(await screen.findByText("Change Password")).toBeInTheDocument();
  });

  it("an authenticated user without the alerts capability sees the access-denied panel instead of the alerts page", async () => {
    sessionWith({ resourceAccess: ["incidents"] });
    renderApp("/alerts");
    expect(await screen.findByText(/doesn't have access to this area/)).toBeInTheDocument();
  });

  it("a non-admin visiting /settings sees the admin-only panel", async () => {
    sessionWith({ role: "analyst" });
    renderApp("/settings/webhooks");
    expect(await screen.findByText(/restricted to administrators/)).toBeInTheDocument();
  });

  it("an admin can reach /settings", async () => {
    sessionWith({ role: "admin" });
    renderApp("/settings/webhooks");
    expect(await screen.findByRole("heading", { name: "Settings" })).toBeInTheDocument();
    expect(screen.queryByText(/restricted to administrators/)).not.toBeInTheDocument();
  });

  it("a fully-authorized user reaches the Dashboard", async () => {
    sessionWith();
    renderApp("/dashboard");
    expect(await screen.findByRole("heading", { name: "Security Operations Dashboard" })).toBeInTheDocument();
  });
});
