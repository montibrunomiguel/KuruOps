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

  it("an expiry far in the future shows the plain expiry-date badge", async () => {
    const farFuture = new Date(Date.now() + 300 * 86_400_000).toISOString();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([endpointFixture({ expiresAt: farFuture })])));
    renderPanel();

    expect(await screen.findByText(/^Expires /)).toBeInTheDocument();
  });

  it("shows the error banner when the initial list fetch fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "list boom" }, 500)));
    renderPanel();

    expect(await screen.findByText("list boom")).toBeInTheDocument();
  });

  it("Cancel on the create form dismisses it without submitting", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No webhook endpoints configured yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Endpoint" }));
    expect(screen.getByLabelText("Name")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByLabelText("Name")).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/webhooks", expect.objectContaining({ method: "POST" }));
  });

  it("shows an error message when creating an endpoint fails", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ error: "create boom" }, 400));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No webhook endpoints configured yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Endpoint" }));
    await userEvent.type(screen.getByLabelText("Name"), "Wazuh Prod");
    await userEvent.type(screen.getByLabelText("Source"), "wazuh");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("create boom")).toBeInTheDocument();
  });

  it("selecting 'Never expires' on create sends expiresInDays: 0", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ endpoint: endpointFixture(), token: "whk_x" }, 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No webhook endpoints configured yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Endpoint" }));
    await userEvent.type(screen.getByLabelText("Name"), "Wazuh Prod");
    await userEvent.type(screen.getByLabelText("Source"), "wazuh");
    await userEvent.selectOptions(screen.getByLabelText("Token expiry"), "never");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "POST");
      expect(postCall).toBeDefined();
      const body = JSON.parse((postCall![1] as RequestInit).body as string);
      expect(body.expiresInDays).toBe(0);
    });
  });

  it("Copy writes the revealed token to the clipboard, and Close dismisses the reveal banner", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
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
    await screen.findByText("whk_supersecretvalue");

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith("whk_supersecretvalue");

    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByText("whk_supersecretvalue")).not.toBeInTheDocument();
  });

  it("toggling status posts to the enable endpoint for a disabled token", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/enable")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([endpointFixture({ status: "disabled" })]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Wazuh Prod");
    expect(screen.getByText("disabled")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Enable" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/webhooks/e1/enable", expect.objectContaining({ method: "POST" })),
    );
  });

  it("shows an error message when toggling status fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/disable")) return Promise.resolve(jsonResponse({ error: "toggle boom" }, 500));
        return Promise.resolve(jsonResponse([endpointFixture()]));
      }),
    );
    renderPanel();
    await screen.findByText("Wazuh Prod");

    await userEvent.click(screen.getByRole("button", { name: "Disable" }));
    expect(await screen.findByText("toggle boom")).toBeInTheDocument();
  });

  it("Regenerate posts to the regenerate endpoint with the chosen expiry and reveals the new token", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/regenerate")) return Promise.resolve(jsonResponse({ token: "whk_newtoken" }));
      return Promise.resolve(jsonResponse([endpointFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Wazuh Prod");

    await userEvent.selectOptions(screen.getByTitle("New token expiry"), "30");
    await userEvent.click(screen.getByRole("button", { name: "Regenerate" }));

    // The reveal is lifted to the panel-level banner (like the create flow)
    // rather than held in the row's own state -- the row unmounts during
    // the reload that immediately follows, which would otherwise destroy
    // a locally-held token before it's ever shown.
    expect(await screen.findByText(/whk_newtoken/)).toBeInTheDocument();
    await waitFor(() => {
      const regenCall = fetchMock.mock.calls.find((c) => (c[0] as string).includes("/regenerate"));
      expect(regenCall).toBeDefined();
      const body = JSON.parse((regenCall![1] as RequestInit).body as string);
      expect(body.expiresInDays).toBe(30);
    });
  });

  it("shows an error message when regenerate fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/regenerate")) return Promise.resolve(jsonResponse({ error: "regenerate boom" }, 500));
        return Promise.resolve(jsonResponse([endpointFixture()]));
      }),
    );
    renderPanel();
    await screen.findByText("Wazuh Prod");

    await userEvent.click(screen.getByRole("button", { name: "Regenerate" }));
    expect(await screen.findByText("regenerate boom")).toBeInTheDocument();
  });
});
