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

function tagFixture(name: string) {
  return { id: name, tenantId: "t1", name, createdAt: "2024-01-01" };
}

// routeFetch handles GET /settings/roles and GET /tags explicitly so
// TagPicker (which sources its options from the tag catalog) never
// accidentally receives role fixtures as if they were tags.
function routeFetch(roles: unknown[], tags: unknown[] = []) {
  return vi.fn().mockImplementation((url: string) => {
    if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse(tags));
    if (url.includes("/settings/roles")) return Promise.resolve(jsonResponse(roles));
    return Promise.resolve(jsonResponse([]));
  });
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
    vi.stubGlobal("fetch", routeFetch([roleFixture()]));
    renderPanel();

    expect(await screen.findByText("Analyst")).toBeInTheDocument();
    expect(screen.getByText(/Alerts, Incidents/)).toBeInTheDocument();
  });

  it("shows an admin badge for an admin role", async () => {
    vi.stubGlobal("fetch", routeFetch([roleFixture({ isAdmin: true })]));
    renderPanel();

    expect(await screen.findByText("Analyst")).toBeInTheDocument();
    expect(screen.getByText("Admin")).toBeInTheDocument();
  });

  it("shows a role's allowed tags as chips", async () => {
    vi.stubGlobal("fetch", routeFetch([roleFixture({ allowedTags: ["Company: Acme Corp", "Company: Globex"] })]));
    renderPanel();

    expect(await screen.findByText(/Company: Acme Corp, Company: Globex/)).toBeInTheDocument();
  });

  it("shows the empty state when no roles exist", async () => {
    vi.stubGlobal("fetch", routeFetch([]));
    renderPanel();

    expect(await screen.findByText("No roles yet.")).toBeInTheDocument();
  });

  it("creating a role POSTs name/isAdmin/resourceAccess/allowedTags", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(roleFixture({ id: "r2", name: "SOC L1" }), 201));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
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

  it("picking tags from the catalog adds them to allowedTags on save", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(roleFixture({ id: "r2", name: "SOC L1" }), 201));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([tagFixture("Company: Acme Corp"), tagFixture("Company: Globex")]));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No roles yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Role" }));
    await userEvent.type(screen.getByLabelText("Name"), "SOC L1");

    await userEvent.selectOptions(await screen.findByLabelText("+ Add tag..."), "Company: Acme Corp");
    await userEvent.selectOptions(screen.getByLabelText("+ Add tag..."), "Company: Globex");
    expect(screen.getByText("Company: Acme Corp")).toBeInTheDocument();
    expect(screen.getByText("Company: Globex")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/roles",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            name: "SOC L1", isAdmin: false, resourceAccess: ["alerts", "incidents"],
            allowedTags: ["Company: Acme Corp", "Company: Globex"],
          }),
        }),
      ),
    );
  });

  it("editing a role PUTs the updated values", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(roleFixture({ name: "Senior Analyst" })));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
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
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
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
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(jsonResponse({ error: "role is assigned to 2 user(s), reassign them before deleting" }, 409));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
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
