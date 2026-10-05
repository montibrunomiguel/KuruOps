import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MCPServersPanel } from "../MCPServersPanel";
import { AuthProvider } from "../../../auth/AuthContext";
import { EMPTY_AUTH_DRAFT, authPayload } from "./AuthFields";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function serverFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "s1", name: "Threat Intel", transport: "http", endpointOrCommand: "https://mcp.example.com",
    authType: "none", isEnabled: true, allowedTools: ["lookup_ip"], sideEffectingTools: [], enabledFor: ["alert_analysis"],
    ...overrides,
  };
}

function renderPanel() {
  return render(
    <AuthProvider>
      <MCPServersPanel />
    </AuthProvider>,
  );
}

function lastBody(fetchMock: ReturnType<typeof vi.fn>, method: string): Record<string, unknown> {
  const calls = fetchMock.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === method);
  const call = calls[calls.length - 1];
  expect(call, `a ${method} request was made`).toBeDefined();
  return JSON.parse((call![1] as RequestInit).body as string);
}

describe("authPayload", () => {
  it("none sends only the type", () => {
    expect(authPayload(EMPTY_AUTH_DRAFT)).toEqual({ authType: "none" });
  });

  it("ignores fields that belong to other types, even if they hold stale text", () => {
    const payload = authPayload({ ...EMPTY_AUTH_DRAFT, type: "bearer", bearerToken: "t", apiKey: "stale", oauthClientSecret: "stale" });
    expect(payload).toEqual({ authType: "bearer", bearerToken: "t" });
  });

  it("omits a blank secret instead of sending an empty string (blank means keep)", () => {
    expect(authPayload({ ...EMPTY_AUTH_DRAFT, type: "api_key", apiKeyHeader: "X-Key" })).toEqual({ authType: "api_key", apiKeyHeader: "X-Key" });
    expect(authPayload({ ...EMPTY_AUTH_DRAFT, type: "bearer" })).toEqual({ authType: "bearer" });
    expect(authPayload({ ...EMPTY_AUTH_DRAFT, type: "oauth", oauthTokenUrl: "https://a/t", oauthClientId: "c" })).toEqual({
      authType: "oauth", oauthTokenUrl: "https://a/t", oauthClientId: "c",
    });
  });

  it("trims the header name, token URL and client id but never touches a secret", () => {
    expect(authPayload({ ...EMPTY_AUTH_DRAFT, type: "api_key", apiKeyHeader: " X-Key ", apiKey: " k " })).toEqual({
      authType: "api_key", apiKeyHeader: "X-Key", apiKey: " k ",
    });
  });
});

describe("MCP server authentication form", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  function postMock() {
    return vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST" && url === "/api/v1/settings/mcp-servers") return Promise.resolve(jsonResponse(serverFixture(), 201));
      return Promise.resolve(jsonResponse([]));
    });
  }

  async function openNewServerForm() {
    await waitFor(() => expect(screen.getByText("No MCP server registered yet.")).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "+ New Server" }));
    await userEvent.type(screen.getByLabelText("Name"), "Srv");
    await userEvent.type(screen.getByLabelText(/Endpoint URL/), "https://mcp.example.com");
  }

  it("defaults to no authentication and shows no credential inputs", async () => {
    vi.stubGlobal("fetch", postMock());
    renderPanel();
    await openNewServerForm();

    expect(screen.getByLabelText("Authentication")).toHaveValue("none");
    expect(screen.queryByLabelText("Token")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("API key")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Client secret")).not.toBeInTheDocument();
  });

  it("API key: asks for the header and the key, and posts them", async () => {
    const fetchMock = postMock();
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await openNewServerForm();

    await userEvent.selectOptions(screen.getByLabelText("Authentication"), "api_key");
    await userEvent.type(screen.getByLabelText("Header"), "X-API-Key");
    await userEvent.type(screen.getByLabelText("API key"), "k-123");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers", expect.objectContaining({ method: "POST" })));
    const body = lastBody(fetchMock, "POST");
    expect(body).toMatchObject({ authType: "api_key", apiKeyHeader: "X-API-Key", apiKey: "k-123" });
    expect(body).not.toHaveProperty("bearerToken");
    expect(body).not.toHaveProperty("authToken");
  });

  it("Bearer token: asks only for the token", async () => {
    const fetchMock = postMock();
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await openNewServerForm();

    await userEvent.selectOptions(screen.getByLabelText("Authentication"), "bearer");
    expect(screen.queryByLabelText("Header")).not.toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("Token"), "tok-9");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers", expect.objectContaining({ method: "POST" })));
    expect(lastBody(fetchMock, "POST")).toMatchObject({ authType: "bearer", bearerToken: "tok-9" });
  });

  it("OAuth: asks for the auth server, client id and client secret", async () => {
    const fetchMock = postMock();
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await openNewServerForm();

    await userEvent.selectOptions(screen.getByLabelText("Authentication"), "oauth");
    await userEvent.type(screen.getByLabelText("Authorization server (token URL)"), "https://auth.example.com/oauth/token");
    await userEvent.type(screen.getByLabelText("Client ID"), "my-client");
    await userEvent.type(screen.getByLabelText("Client secret"), "s3cr3t");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers", expect.objectContaining({ method: "POST" })));
    expect(lastBody(fetchMock, "POST")).toMatchObject({
      authType: "oauth", oauthTokenUrl: "https://auth.example.com/oauth/token", oauthClientId: "my-client", oauthClientSecret: "s3cr3t",
    });
  });

  it("every secret input is masked, never autofilled, and required on create", async () => {
    vi.stubGlobal("fetch", postMock());
    renderPanel();
    await openNewServerForm();

    for (const [type, label] of [["api_key", "API key"], ["bearer", "Token"], ["oauth", "Client secret"]] as const) {
      await userEvent.selectOptions(screen.getByLabelText("Authentication"), type);
      const input = screen.getByLabelText(label);
      expect(input).toHaveAttribute("type", "password");
      expect(input).toHaveAttribute("autocomplete", "new-password");
      expect(input).toBeRequired();
    }
  });

  it("switching the type clears the previous type's request fields", async () => {
    const fetchMock = postMock();
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await openNewServerForm();

    await userEvent.selectOptions(screen.getByLabelText("Authentication"), "bearer");
    await userEvent.type(screen.getByLabelText("Token"), "leftover");
    await userEvent.selectOptions(screen.getByLabelText("Authentication"), "none");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers", expect.objectContaining({ method: "POST" })));
    const body = lastBody(fetchMock, "POST");
    expect(body.authType).toBe("none");
    expect(body).not.toHaveProperty("bearerToken");
  });
});

describe("editing a saved server's authentication", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  function editMock(server: unknown) {
    return vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(server));
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse([server]));
    });
  }

  it("the row shows the auth type and its non-secret parameters", async () => {
    vi.stubGlobal(
      "fetch",
      editMock(serverFixture({ authType: "api_key", authHeaderName: "X-Api-Key" })),
    );
    renderPanel();

    expect(await screen.findByText(/auth: API key · X-Api-Key/)).toBeInTheDocument();
  });

  it("starts from the saved type with every secret blank and optional, and a blank secret is not sent", async () => {
    const server = serverFixture({
      authType: "oauth", oauthTokenUrl: "https://auth.example.com/token", oauthClientId: "my-client",
    });
    const fetchMock = editMock(server);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Authentication" }));

    expect(screen.getByLabelText("Authentication")).toHaveValue("oauth");
    expect(screen.getByLabelText("Authorization server (token URL)")).toHaveValue("https://auth.example.com/token");
    expect(screen.getByLabelText("Client ID")).toHaveValue("my-client");
    const secret = screen.getByLabelText("Client secret");
    expect(secret).toHaveValue("");
    expect(secret).not.toBeRequired();
    expect(screen.getByText(/Leave blank to keep the current value/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers/s1", expect.objectContaining({ method: "PUT" })));
    const body = lastBody(fetchMock, "PUT");
    expect(body).toMatchObject({
      name: "Threat Intel", endpointOrCommand: "https://mcp.example.com", allowedTools: ["lookup_ip"],
      authType: "oauth", oauthTokenUrl: "https://auth.example.com/token", oauthClientId: "my-client",
    });
    expect(body).not.toHaveProperty("oauthClientSecret");
  });

  it("typing a new secret rotates it", async () => {
    const fetchMock = editMock(serverFixture({ authType: "bearer" }));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Authentication" }));
    await userEvent.type(screen.getByLabelText("Token"), "rotated");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers/s1", expect.objectContaining({ method: "PUT" })));
    expect(lastBody(fetchMock, "PUT")).toMatchObject({ authType: "bearer", bearerToken: "rotated" });
  });

  it("switching to a different type makes the new secret required (the old one can't be reinterpreted)", async () => {
    vi.stubGlobal("fetch", editMock(serverFixture({ authType: "bearer" })));
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Authentication" }));
    await userEvent.selectOptions(screen.getByLabelText("Authentication"), "api_key");

    expect(screen.getByLabelText("API key")).toBeRequired();
    expect(screen.queryByText(/Leave blank to keep the current value/)).not.toBeInTheDocument();
  });

  it("switching to None sends authType none", async () => {
    const fetchMock = editMock(serverFixture({ authType: "bearer" }));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Authentication" }));
    await userEvent.selectOptions(screen.getByLabelText("Authentication"), "none");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/mcp-servers/s1", expect.objectContaining({ method: "PUT" })));
    expect(lastBody(fetchMock, "PUT")).toMatchObject({ authType: "none" });
  });

  it("shows the server's error message when the save is rejected", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse({ error: "oauthTokenUrl: must use https" }, 400));
      if (url.includes("/tool-calls")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse([serverFixture({ authType: "bearer" })]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Authentication" }));
    await userEvent.type(screen.getByLabelText("Token"), "x");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("oauthTokenUrl: must use https")).toBeInTheDocument();
  });
});
