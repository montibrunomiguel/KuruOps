import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { IncidentSLAPanel } from "./IncidentSLAPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <IncidentSLAPanel />
    </AuthProvider>,
  );
}

const configuredPolicy = {
  id: "p1", tenantId: "t1", severity: "critical", priority: "p1", dueWithinMinutes: 60,
  createdAt: "2024-01-01", updatedAt: "2024-01-01",
};

describe("IncidentSLAPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders an empty grid and a disabled Save button when nothing is configured", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderPanel();

    expect(await screen.findByLabelText("Critical / P1")).toHaveValue(null);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("loads an existing policy into its cell", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([configuredPolicy])));
    renderPanel();

    expect(await screen.findByLabelText("Critical / P1")).toHaveValue(60);
  });

  it("editing a cell enables Save, and saving PUTs the changed policy", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse({ ...configuredPolicy, dueWithinMinutes: 30 }));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const cell = await screen.findByLabelText("Critical / P1");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();

    await userEvent.type(cell, "30");
    expect(screen.getByRole("button", { name: "Save" })).not.toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/incident-sla",
        expect.objectContaining({ method: "PUT" }),
      ),
    );
    expect(await screen.findByText("Changes saved.")).toBeInTheDocument();
  });

  it("clearing a configured cell deletes the policy", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([configuredPolicy]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const cell = await screen.findByLabelText("Critical / P1");
    expect(cell).toHaveValue(60);

    await userEvent.clear(cell);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/incident-sla/p1",
        expect.objectContaining({ method: "DELETE" }),
      ),
    );
  });

  it("shows the server's error message on a failed load", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "internal error" }, 500)));
    renderPanel();

    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });
});
