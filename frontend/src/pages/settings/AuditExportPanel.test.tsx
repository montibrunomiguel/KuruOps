import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuditExportPanel } from "./AuditExportPanel";
import { AuthProvider } from "../../auth/AuthContext";

function renderPanel() {
  return render(
    <AuthProvider>
      <AuditExportPanel />
    </AuthProvider>,
  );
}

describe("AuditExportPanel", () => {
  beforeEach(() => {
    localStorage.clear();
    // jsdom doesn't implement the Blob-URL APIs the download flow uses.
    vi.stubGlobal("URL", { ...URL, createObjectURL: vi.fn(() => "blob:mock"), revokeObjectURL: vi.fn() });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("clicking Export as CEF fetches the export endpoint and triggers a download", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response("CEF:0|...", {
        status: 200,
        headers: {
          "content-type": "text/plain; charset=utf-8",
          "content-disposition": 'attachment; filename="argusops-audit-20260806T000000Z.cef.log"',
        },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    renderPanel();
    await userEvent.click(screen.getByRole("button", { name: "Export as CEF" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/audit-export/cef",
        expect.objectContaining({ headers: expect.any(Object) }),
      ),
    );
    expect(clickSpy).toHaveBeenCalled();

    clickSpy.mockRestore();
  });

  it("clicking Export as JSON fetches the json export endpoint and triggers a download", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('{"eventId":"1"}\n', {
        status: 200,
        headers: {
          "content-type": "application/x-ndjson; charset=utf-8",
          "content-disposition": 'attachment; filename="argusops-audit-20260806T000000Z.ndjson"',
        },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    renderPanel();
    await userEvent.click(screen.getByRole("button", { name: "Export as JSON" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/audit-export/json",
        expect.objectContaining({ headers: expect.any(Object) }),
      ),
    );
    expect(clickSpy).toHaveBeenCalled();

    clickSpy.mockRestore();
  });

  it("shows the server's error message on a failed export", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "internal error" }), {
          status: 500,
          headers: { "content-type": "application/json" },
        }),
      ),
    );
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "Export as CEF" }));
    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });
});
