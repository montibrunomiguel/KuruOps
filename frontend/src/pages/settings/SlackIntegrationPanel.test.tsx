import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { SlackIntegrationPanel } from "./SlackIntegrationPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

// useSearchParams needs a Router context, hence MemoryRouter -- same
// reasoning as StorageIntegrationPanel.test.tsx's renderPanel.
function renderPanel(initialPath = "/") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <AuthProvider>
        <SlackIntegrationPanel />
      </AuthProvider>
    </MemoryRouter>,
  );
}

const connectedConfig = {
  tenantId: "t1",
  teamId: "T123",
  teamName: "Acme Corp",
  botUserId: "U456",
  installedByUserId: "u1",
  installedByUserName: "Jane Admin",
  grantedScopes: "chat:write,channels:read",
  createdAt: "2026-08-01T00:00:00Z",
  updatedAt: "2026-08-01T00:00:00Z",
};

describe("SlackIntegrationPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows the connect button and no badge when nothing is connected yet", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    expect(await screen.findByRole("button", { name: "Connect to Slack" })).toBeInTheDocument();
    expect(screen.queryByText(/Connected to/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Disconnect" })).not.toBeInTheDocument();
  });

  it("shows the workspace, installer, and Disconnect button once connected", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(connectedConfig)));
    renderPanel();

    expect(await screen.findByText("Connected to Acme Corp")).toBeInTheDocument();
    expect(screen.getByText("Acme Corp")).toBeInTheDocument();
    expect(screen.getByText("Jane Admin")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Disconnect" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Connect to Slack" })).not.toBeInTheDocument();
  });

  it("clicking Connect fetches the authorize URL and redirects the page", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (typeof url === "string" && url.includes("authorize-url")) {
        return Promise.resolve(jsonResponse({ url: "https://slack.com/oauth/v2/authorize?state=abc" }));
      }
      return Promise.resolve(jsonResponse(null));
    });
    vi.stubGlobal("fetch", fetchMock);

    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...originalLocation, href: "" },
    });

    renderPanel();
    await userEvent.click(await screen.findByRole("button", { name: "Connect to Slack" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/integrations/slack/oauth/authorize-url",
        expect.anything(),
      ),
    );
    await waitFor(() => expect(window.location.href).toBe("https://slack.com/oauth/v2/authorize?state=abc"));

    Object.defineProperty(window, "location", { configurable: true, value: originalLocation });
  });

  it("disconnecting requires inline confirmation, then DELETEs and clears the connected state", async () => {
    let disconnected = false;
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") {
        disconnected = true;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.resolve(jsonResponse(disconnected ? null : connectedConfig));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
    await userEvent.click(await screen.findByRole("button", { name: "Confirm delete" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/integrations/slack", expect.objectContaining({ method: "DELETE" })),
    );
    expect(await screen.findByRole("button", { name: "Connect to Slack" })).toBeInTheDocument();
  });

  it("surfaces a slack_error query param from the OAuth redirect as an error banner", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel("/?slack_error=access_denied");

    expect(await screen.findByText("access_denied")).toBeInTheDocument();
  });

  it("surfaces a slack_connected query param from the OAuth redirect as a success message", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(connectedConfig)));
    renderPanel("/?slack_connected=1");

    expect(await screen.findByText("Workspace connected.")).toBeInTheDocument();
  });

  it("a fetch error surfaces the error banner", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "internal error" }, 500)));
    renderPanel();

    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });
});
