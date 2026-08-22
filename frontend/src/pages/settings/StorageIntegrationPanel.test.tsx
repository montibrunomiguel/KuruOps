import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { StorageIntegrationPanel } from "./StorageIntegrationPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

// initialPath lets a test simulate landing back on this page after the
// Google Drive OAuth redirect (?gdrive_connected=1 / ?gdrive_error=...) --
// useSearchParams needs a Router context, hence MemoryRouter here (this
// panel is otherwise router-agnostic, unlike AlertsListPage).
function renderPanel(initialPath = "/") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <AuthProvider>
        <StorageIntegrationPanel />
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("StorageIntegrationPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows 'Configure' and the S3 tab by default when nothing is set up yet", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    expect(await screen.findByRole("button", { name: "Configure" })).toBeInTheDocument();
    expect(screen.getByLabelText("Bucket")).toBeInTheDocument();
    expect(screen.queryByText(/configured$/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove integration" })).not.toBeInTheDocument();
  });

  it("secret access key is required only when no config exists yet", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    await screen.findByRole("button", { name: "Configure" });
    expect(screen.getByLabelText(/Secret Access Key/)).toBeRequired();
  });

  it("shows 'Update' and a badge once S3 is configured, secret no longer required", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          provider: "s3", s3Bucket: "evidence", s3Region: "us-east-1", s3AccessKeyId: "AKIA",
        }),
      ),
    );
    renderPanel();

    expect(await screen.findByRole("button", { name: "Update" })).toBeInTheDocument();
    expect(screen.getByText("S3 configured")).toBeInTheDocument();
    expect(screen.getByLabelText(/Secret Access Key/)).not.toBeRequired();
    expect(screen.getByRole("button", { name: "Remove integration" })).toBeInTheDocument();
  });

  it("saving the S3 form PUTs the config and shows a success message", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(null));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.type(await screen.findByLabelText("Bucket"), "evidence");
    await userEvent.type(screen.getByLabelText("Region"), "us-east-1");
    await userEvent.type(screen.getByLabelText("Access Key ID"), "AKIA");
    await userEvent.type(screen.getByLabelText(/Secret Access Key/), "s3cret");
    await userEvent.click(screen.getByRole("button", { name: "Configure" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/storage/s3", expect.objectContaining({ method: "PUT" })),
    );
    expect(await screen.findByText("Configuration saved.")).toBeInTheDocument();
  });

  it("switching to the GCS tab shows GCS fields instead", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    await screen.findByLabelText("Bucket");
    expect(screen.queryByLabelText(/Service Account Credentials/)).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Google Cloud Storage" }));
    expect(screen.getByLabelText(/Service Account Credentials/)).toBeInTheDocument();
    expect(screen.getByLabelText("Project ID")).toBeInTheDocument();
  });

  it("removing the integration DELETEs and clears the configured badge", async () => {
    let removed = false;
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") {
        removed = true;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.resolve(
        jsonResponse(removed ? null : { provider: "s3", s3Bucket: "evidence", s3Region: "us-east-1", s3AccessKeyId: "AKIA" }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Remove integration" }));
    await userEvent.click(await screen.findByRole("button", { name: "Confirm delete" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/storage", expect.objectContaining({ method: "DELETE" })),
    );
    expect(screen.queryByText("S3 configured")).not.toBeInTheDocument();
  });

  it("a fetch error surfaces the error banner", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "internal error" }, 500)));
    renderPanel();

    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });

  it("switching to the Google Drive tab defaults to the service account sub-method", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    await screen.findByLabelText("Bucket");
    await userEvent.click(screen.getByRole("button", { name: "Google Drive" }));

    expect(screen.getByLabelText("Folder ID")).toBeInTheDocument();
    expect(screen.getByLabelText(/Service Account Credentials/)).toBeInTheDocument();
    expect(screen.queryByText("You'll be redirected to Google to grant access, then brought back here.")).not.toBeInTheDocument();
  });

  it("saving Google Drive via service account PUTs the folder ID and JSON key", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(null));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await screen.findByLabelText("Bucket");
    await userEvent.click(screen.getByRole("button", { name: "Google Drive" }));
    await userEvent.type(screen.getByLabelText("Folder ID"), "folder-123");
    await userEvent.type(screen.getByLabelText(/Service Account Credentials/), '{{"type":"service_account"}}');
    await userEvent.click(screen.getByRole("button", { name: "Configure" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/storage/gdrive/service-account",
        expect.objectContaining({ method: "PUT" }),
      ),
    );
    expect(await screen.findByText("Configuration saved.")).toBeInTheDocument();
  });

  it("Google Drive OAuth sub-tab shows the redirect hint instead of a credentials field", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    await screen.findByLabelText("Bucket");
    await userEvent.click(screen.getByRole("button", { name: "Google Drive" }));
    await userEvent.click(screen.getByRole("button", { name: "Connect Google account" }));

    expect(screen.getByText("You'll be redirected to Google to grant access, then brought back here.")).toBeInTheDocument();
    expect(screen.queryByLabelText(/Service Account Credentials/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Connect with Google" })).toBeInTheDocument();
  });

  it("Google Drive OAuth sub-tab redirects the page to the returned authorize URL", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (typeof url === "string" && url.includes("authorize-url")) {
        return Promise.resolve(jsonResponse({ url: "https://accounts.google.com/o/oauth2/auth?state=abc" }));
      }
      return Promise.resolve(jsonResponse(null));
    });
    vi.stubGlobal("fetch", fetchMock);

    const originalLocation = window.location;
    // jsdom throws on direct assignment to window.location.href; replace the
    // whole object for this test only, matching the pattern used wherever
    // this codebase asserts on a real page navigation.
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...originalLocation, href: "" },
    });

    renderPanel();
    await screen.findByLabelText("Bucket");
    await userEvent.click(screen.getByRole("button", { name: "Google Drive" }));
    await userEvent.click(screen.getByRole("button", { name: "Connect Google account" }));
    await userEvent.type(screen.getByLabelText("Folder ID"), "folder-123");
    await userEvent.click(screen.getByRole("button", { name: "Connect with Google" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/v1/settings/storage/gdrive/oauth/authorize-url?folderId=folder-123"),
        expect.anything(),
      ),
    );
    await waitFor(() => expect(window.location.href).toBe("https://accounts.google.com/o/oauth2/auth?state=abc"));

    Object.defineProperty(window, "location", { configurable: true, value: originalLocation });
  });

  it("shows the connected-as email badge for an OAuth-linked Drive config", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          provider: "gdrive",
          gdriveFolderId: "folder-123",
          gdriveAuthMethod: "oauth",
          gdriveOauthConnectedEmail: "admin@example.com",
        }),
      ),
    );
    renderPanel();

    expect(await screen.findByText("Connected as admin@example.com")).toBeInTheDocument();
  });

  it("surfaces a gdrive_error query param from the OAuth redirect as an error banner", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel("/?gdrive_error=access_denied");

    expect(await screen.findByText("access_denied")).toBeInTheDocument();
  });

  it("surfaces a gdrive_connected query param from the OAuth redirect as a success message", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel("/?gdrive_connected=1");

    expect(await screen.findByText("Configuration saved.")).toBeInTheDocument();
  });
});
