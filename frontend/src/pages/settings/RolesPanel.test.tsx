import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RolesPanel } from "./RolesPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function roleFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "r1", tenantId: "t1", name: "Analyst", isAdmin: false,
    resourceAccess: ["alerts", "incidents"], allowedTags: [], createdAt: "2024-01-01", updatedAt: "2024-01-01", ...overrides,
  };
}

function renderPanel() {
  return render(
    <AuthProvider>
      <RolesPanel />
    </AuthProvider>,
  );
}

describe("RolesPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders every role with its capabilities and tag scope", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([roleFixture()])));
    renderPanel();

    expect(await screen.findByText("Analyst")).toBeInTheDocument();
    expect(screen.getByText(/Alerts, Incidents/)).toBeInTheDocument();
  });

  it("shows an admin badge for an admin role", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([roleFixture({ isAdmin: true })])));
    renderPanel();

    expect(await screen.findByText("Analyst")).toBeInTheDocument();
    expect(screen.getByText("Admin")).toBeInTheDocument();
  });

  it("shows the empty state when no roles exist", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderPanel();

    expect(await screen.findByText("No roles yet.")).toBeInTheDocument();
  });

  it("creating a role POSTs name/isAdmin/resourceAccess/allowedTags", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(roleFixture({ id: "r2", name: "SOC L1" }), 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No roles yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Role" }));
    await userEvent.type(screen.getByLabelText("Name"), "SOC L1");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/roles",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ name: "SOC L1", isAdmin: false, resourceAccess: ["alerts", "incidents"], allowedTags: [] }),
        }),
      ),
    );
  });

  it("editing a role PUTs the updated values", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(roleFixture({ name: "Senior Analyst" })));
      return Promise.resolve(jsonResponse([roleFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Analyst");

    await userEvent.click(screen.getByRole("button", { name: "Edit" }));
    const nameField = screen.getByLabelText("Name");
    await userEvent.clear(nameField);
    await userEvent.type(nameField, "Senior Analyst");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/roles/r1",
        expect.objectContaining({ method: "PUT" }),
      ),
    );
  });

  it("delete requires an inline confirm click, then DELETEs", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([roleFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Analyst");

    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/roles/r1", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/roles/r1", expect.objectContaining({ method: "DELETE" })),
    );
  });

  it("shows the server's conflict error when deleting a role still in use", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(jsonResponse({ error: "role is assigned to 2 user(s), reassign them before deleting" }, 409));
      return Promise.resolve(jsonResponse([roleFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Analyst");

    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));

    expect(await screen.findByText(/assigned to 2 user/)).toBeInTheDocument();
  });
});
