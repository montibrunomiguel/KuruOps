import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { WebhooksPanel } from "./WebhooksPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function endpointFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "e1", name: "Wazuh Prod", source: "wazuh", status: "active",
    tokenLast4: "ab12", expiresAt: "2026-06-01T00:00:00Z", createdAt: "2026-01-01T00:00:00Z", ...overrides,
  };
}

function renderPanel() {
  return render(
    <AuthProvider>
      <WebhooksPanel />
    </AuthProvider>,
  );
}

describe("WebhooksPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("lists existing endpoints with a masked token", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([endpointFixture()])));
    renderPanel();

    expect(await screen.findByText("Wazuh Prod")).toBeInTheDocument();
    expect(screen.getByText(/whk_••••••••ab12/)).toBeInTheDocument();
  });

  it("shows the empty state with none configured", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderPanel();

    expect(await screen.findByText("No webhook endpoints configured yet.")).toBeInTheDocument();
  });

  it("creating an endpoint reveals the one-time plaintext token", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ endpoint: endpointFixture(), token: "whk_supersecretvalue" }, 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No webhook endpoints configured yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Endpoint" }));
    await userEvent.type(screen.getByLabelText("Name"), "Wazuh Prod");
    await userEvent.type(screen.getByLabelText("Source"), "wazuh");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("whk_supersecretvalue")).toBeInTheDocument();
  });

  it("toggling status posts to the disable endpoint for an active token", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/disable")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([endpointFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Wazuh Prod");

    await userEvent.click(screen.getByRole("button", { name: "Disable" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/webhooks/e1/disable", expect.objectContaining({ method: "POST" })),
    );
  });

  it("an endpoint expiring soon shows the warning badge", async () => {
    const soon = new Date(Date.now() + 5 * 86_400_000).toISOString();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([endpointFixture({ expiresAt: soon })])));
    renderPanel();

    expect(await screen.findByText(/Expiring soon/)).toBeInTheDocument();
  });

  it("an already-expired endpoint shows the expired badge", async () => {
    const past = new Date(Date.now() - 86_400_000).toISOString();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([endpointFixture({ expiresAt: past })])));
    renderPanel();

    expect(await screen.findByText(/Expired on/)).toBeInTheDocument();
  });

  it("no expiresAt shows 'Never expires'", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([endpointFixture({ expiresAt: undefined })])));
    renderPanel();

    await screen.findByText("Wazuh Prod");
    // "Never expires" also appears as a <select> option (expiry picker), so
    // the assertion targets the badge specifically, not just any match.
    expect(screen.getByText("Never expires", { selector: "span" })).toBeInTheDocument();
  });
});
