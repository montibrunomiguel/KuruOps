import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StorageIntegrationPanel } from "./StorageIntegrationPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <StorageIntegrationPanel />
    </AuthProvider>,
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
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(
        jsonResponse({ provider: "s3", s3Bucket: "evidence", s3Region: "us-east-1", s3AccessKeyId: "AKIA" }),
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
});
