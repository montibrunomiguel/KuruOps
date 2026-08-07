import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MCPServersPanel } from "./MCPServersPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function serverFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "s1", name: "Threat Intel", transport: "http", endpointOrCommand: "https://mcp.example.com",
    isEnabled: true, allowedTools: ["lookup_ip"], sideEffectingTools: [], enabledFor: ["alert_analysis"], ...overrides,
  };
}

function renderPanel() {
  return render(
    <AuthProvider>
      <MCPServersPanel />
    </AuthProvider>,
  );
}

// routeFetch backs most tests below: MCPServersPanel always fetches both
// the server list and the pending-tool-calls list (PendingApprovalsPanel),
// so a mock that returns the same body for every URL would mis-serve one
// of them as the other -- see the "no pending approvals" default here.
function routeFetch(servers: unknown[], toolCalls: unknown[] = []) {
  return vi.fn().mockImplementation((url: string) => {
    if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse(toolCalls));
    if (url.includes("/mcp-servers")) return Promise.resolve(jsonResponse(servers));
    return Promise.resolve(jsonResponse([]));
  });
}

describe("MCPServersPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("lists servers with their allow-listed tools", async () => {
    vi.stubGlobal("fetch", routeFetch([serverFixture()]));
    renderPanel();

    expect(await screen.findByText("Threat Intel")).toBeInTheDocument();
    expect(screen.getByText("lookup_ip")).toBeInTheDocument();
  });

  it("rejects a side-effecting tool that isn't in the allow-list, without calling the API", async () => {
    const fetchMock = routeFetch([]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No MCP server registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Server" }));
    await userEvent.type(screen.getByLabelText("Name"), "Test Server");
    await userEvent.type(screen.getByLabelText(/Endpoint URL/), "https://mcp.example.com");
    await userEvent.type(screen.getByLabelText(/Allowed tools/), "lookup_ip");
    await userEvent.type(screen.getByLabelText(/Side-effecting tools/), "quarantine_host");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText(/quarantine_host/, { selector: ".error-banner" })).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/mcp-servers", expect.objectContaining({ method: "POST" }));
  });

  it("a valid submission posts the parsed tool lists", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST" && url === "/api/v1/settings/mcp-servers") return Promise.resolve(jsonResponse(serverFixture(), 201));
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No MCP server registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Server" }));
    await userEvent.type(screen.getByLabelText("Name"), "Test Server");
    await userEvent.type(screen.getByLabelText(/Endpoint URL/), "https://mcp.example.com");
    await userEvent.type(screen.getByLabelText(/Allowed tools/), "lookup_ip, quarantine_host");
    await userEvent.type(screen.getByLabelText(/Side-effecting tools/), "quarantine_host");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/mcp-servers",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            name: "Test Server", transport: "http", endpointOrCommand: "https://mcp.example.com",
            authToken: undefined, allowedTools: ["lookup_ip", "quarantine_host"], sideEffectingTools: ["quarantine_host"],
            enabledFor: ["alert_analysis", "incident_analysis"],
          }),
        }),
      ),
    );
  });

  it("Discover tools is disabled for a non-http transport", async () => {
    vi.stubGlobal("fetch", routeFetch([serverFixture({ transport: "stdio" })]));
    renderPanel();

    expect(await screen.findByRole("button", { name: "Discover tools" })).toBeDisabled();
  });

  it("clicking Discover tools calls the discover endpoint and renders results", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/discover-tools")) return Promise.resolve(jsonResponse([{ name: "lookup_domain", description: "Look up a domain" }]));
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse([serverFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Discover tools" }));
    expect(await screen.findByText("lookup_domain")).toBeInTheDocument();
  });

  it("toggling a discovered tool's checkbox and saving PUTs the updated allow-list", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/discover-tools")) {
        return Promise.resolve(jsonResponse([{ name: "lookup_domain", description: "Look up a domain" }]));
      }
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([]));
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(serverFixture(), 200));
      return Promise.resolve(jsonResponse([serverFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Discover tools" }));
    await screen.findByText("lookup_domain");

    const checkbox = screen.getByRole("checkbox", { name: /lookup_domain/ });
    await userEvent.click(checkbox);
    // Marking a tool allowed reveals its own "side-effecting" checkbox --
    // the panel's static helper text also contains that phrase (bolded), so
    // this specifically targets the checkbox, not just any text match.
    const sideEffectingCheckbox = screen.getByRole("checkbox", { name: "side-effecting" });
    await userEvent.click(sideEffectingCheckbox);
    expect(sideEffectingCheckbox).toBeChecked();

    await userEvent.click(screen.getByRole("button", { name: "Save allow-list" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers/s1", expect.objectContaining({ method: "PUT" })),
    );
  });

  it("closing the discover panel hides it again", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/discover-tools")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse([serverFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Discover tools" }));
    expect(await screen.findByText("The server didn't expose any tools.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByText("The server didn't expose any tools.")).not.toBeInTheDocument();
  });

  it("a discover-tools failure shows the error banner", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/discover-tools")) return Promise.resolve(jsonResponse({ error: "connection refused" }, 502));
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse([serverFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Discover tools" }));
    expect(await screen.findByText("connection refused")).toBeInTheDocument();
  });

  it("Remove requires an inline confirm click before deleting", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/mcp-servers/s1") && !url.includes("discover")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([serverFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Threat Intel");

    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/mcp-servers/s1", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers/s1", expect.objectContaining({ method: "DELETE" })),
    );
  });

  it("toggling enable/disable posts to the right action endpoint", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/disable")) return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse([serverFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Threat Intel");

    await userEvent.click(screen.getByRole("button", { name: "Disable" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers/s1/disable", expect.objectContaining({ method: "POST" })),
    );
  });
});

function toolCallFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: 1, tenantId: "t1", mcpServerId: "s1", toolName: "quarantine_host",
    contextType: "alert", contextId: "a1", args: { host: "10.0.0.5" }, status: "proposed",
    createdAt: "2026-01-01T00:00:00Z", ...overrides,
  };
}

describe("MCPServersPanel -- pending tool approvals", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders nothing when there are no pending approvals", async () => {
    vi.stubGlobal("fetch", routeFetch([serverFixture()], []));
    renderPanel();

    await screen.findByText("Threat Intel");
    expect(screen.queryByText("Pending Tool Approvals")).not.toBeInTheDocument();
  });

  it("shows a pending call with its args and resolves the server name", async () => {
    vi.stubGlobal("fetch", routeFetch([serverFixture()], [toolCallFixture()]));
    renderPanel();

    expect(await screen.findByText("Pending Tool Approvals")).toBeInTheDocument();
    expect(screen.getByText("quarantine_host")).toBeInTheDocument();
    expect(screen.getAllByText("Threat Intel").length).toBeGreaterThan(0);
    expect(screen.getByText(/10.0.0.5/)).toBeInTheDocument();
  });

  it("Approve requires an inline confirm click, then posts to the approve endpoint", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/tool-calls/1/approve")) return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([toolCallFixture()]));
      return Promise.resolve(jsonResponse([serverFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Pending Tool Approvals");

    await userEvent.click(screen.getByRole("button", { name: "Approve" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/mcp-servers/tool-calls/1/approve", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/mcp-servers/tool-calls/1/approve",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });

  it("Reject posts to the reject endpoint without a confirm step", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/tool-calls/1/reject")) return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([toolCallFixture()]));
      return Promise.resolve(jsonResponse([serverFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Pending Tool Approvals");

    await userEvent.click(screen.getByRole("button", { name: "Reject" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/mcp-servers/tool-calls/1/reject",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });
});
