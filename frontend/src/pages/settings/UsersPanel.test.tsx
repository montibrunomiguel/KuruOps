import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { UsersPanel } from "./UsersPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function userFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "u1", name: "Ana Lyst", email: "ana@test.local", authProvider: "local",
    role: "analyst", resourceAccess: ["alerts"], allowedTags: [], isActive: true, ...overrides,
  };
}

function mappingFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return { id: "m1", provider: "ldap", externalGroup: "soc-analysts", role: "analyst", resourceAccess: ["alerts"], allowedTags: [], ...overrides };
}

function routeFetch(users: unknown[], mappings: unknown[] = []) {
  return vi.fn().mockImplementation((url: string) => {
    if (url.includes("/group-mappings")) return Promise.resolve(jsonResponse(mappings));
    if (url.includes("/settings/users")) return Promise.resolve(jsonResponse(users));
    return Promise.resolve(jsonResponse({}));
  });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <UsersPanel />
    </AuthProvider>,
  );
}

describe("UsersPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("lists users and their email/provider", async () => {
    vi.stubGlobal("fetch", routeFetch([userFixture()]));
    renderPanel();

    expect(await screen.findByText("Ana Lyst")).toBeInTheDocument();
    expect(screen.getByText(/ana@test.local/)).toBeInTheDocument();
  });

  it("flags an inactive user with a badge", async () => {
    vi.stubGlobal("fetch", routeFetch([userFixture({ isActive: false })]));
    renderPanel();

    expect(await screen.findByText("inactive")).toBeInTheDocument();
  });

  it("Save only appears after a field is changed, and posts the access update", async () => {
    const fetchMock = routeFetch([userFixture()]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Ana Lyst");

    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();

    const roleSelects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(roleSelects[0], "admin");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/users/u1/access", expect.objectContaining({ method: "PUT" })),
    );
  });

  it("Deactivate posts to the deactivate endpoint", async () => {
    const fetchMock = routeFetch([userFixture()]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Ana Lyst");

    await userEvent.click(screen.getByRole("button", { name: "Deactivate" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/users/u1/deactivate", expect.objectContaining({ method: "POST" })),
    );
  });

  it("Revoke sessions requires an inline confirm click, then posts to the revoke-sessions endpoint", async () => {
    const fetchMock = routeFetch([userFixture()]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Ana Lyst");

    await userEvent.click(screen.getByRole("button", { name: "Revoke sessions" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/users/u1/revoke-sessions", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/users/u1/revoke-sessions",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    expect(await screen.findByText("Sessions revoked.")).toBeInTheDocument();
  });

  it("Revoke sessions does nothing when the inline confirm is cancelled", async () => {
    const fetchMock = routeFetch([userFixture()]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Ana Lyst");

    await userEvent.click(screen.getByRole("button", { name: "Revoke sessions" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(fetchMock).not.toHaveBeenCalledWith(
      "/api/v1/settings/users/u1/revoke-sessions",
      expect.anything(),
    );
    expect(screen.getByRole("button", { name: "Revoke sessions" })).toBeInTheDocument();
  });

  it("Reset password requires an inline confirm click, then posts and shows the one-time temp password", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST" && url.endsWith("/reset-password")) {
        return Promise.resolve(jsonResponse({ temporaryPassword: "n3w-temp-pw" }));
      }
      return routeFetch([userFixture()])(url, init);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Ana Lyst");

    await userEvent.click(screen.getByRole("button", { name: "Reset password" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/users/u1/reset-password", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/users/u1/reset-password",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    expect(await screen.findByText("Password reset for Ana Lyst")).toBeInTheDocument();
    expect(screen.getByText("n3w-temp-pw")).toBeInTheDocument();
  });

  it("Reset password does nothing when the inline confirm is cancelled", async () => {
    const fetchMock = routeFetch([userFixture()]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Ana Lyst");

    await userEvent.click(screen.getByRole("button", { name: "Reset password" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/users/u1/reset-password", expect.anything());
  });

  it("Reset password is disabled for a federated user", async () => {
    vi.stubGlobal("fetch", routeFetch([userFixture({ authProvider: "ldap" })]));
    renderPanel();
    await screen.findByText("Ana Lyst");

    expect(screen.getByRole("button", { name: "Reset password" })).toBeDisabled();
  });

  it("lists group mappings", async () => {
    vi.stubGlobal("fetch", routeFetch([], [mappingFixture()]));
    renderPanel();

    expect(await screen.findByText("soc-analysts")).toBeInTheDocument();
  });

  it("shows the empty-mapping hint when none exist", async () => {
    vi.stubGlobal("fetch", routeFetch([], []));
    renderPanel();

    expect(await screen.findByText(/No mapping configured/)).toBeInTheDocument();
  });

  it("creating a group mapping PUTs to the provider/group path", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(mappingFixture(), 200));
      if (url.includes("/group-mappings")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText(/No mapping configured/)).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Mapping" }));
    await userEvent.type(screen.getByLabelText(/Group/), "soc-tier1");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/users/group-mappings/ldap/soc-tier1",
        expect.objectContaining({ method: "PUT" }),
      ),
    );
  });

  it("creating a user posts to /settings/users and shows the one-time temp password", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST" && url === "/api/v1/settings/users") {
        return Promise.resolve(
          jsonResponse({ user: userFixture({ id: "u2", name: "New Hire", email: "new@test.local" }), temporaryPassword: "sup3r-secret-once" }, 201),
        );
      }
      if (url.includes("/group-mappings")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/settings/users")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No users yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New User" }));
    await userEvent.type(screen.getByLabelText("Email"), "new@test.local");
    await userEvent.type(screen.getByLabelText("Name"), "New Hire");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/users",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    expect(await screen.findByText("New Hire created")).toBeInTheDocument();
    expect(screen.getByText("sup3r-secret-once")).toBeInTheDocument();
  });

  it("deleting a mapping requires an inline confirm click", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/group-mappings/m1")) return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/group-mappings")) return Promise.resolve(jsonResponse([mappingFixture()]));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("soc-analysts");

    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/users/group-mappings/m1", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/users/group-mappings/m1",
        expect.objectContaining({ method: "DELETE" }),
      ),
    );
  });
});
