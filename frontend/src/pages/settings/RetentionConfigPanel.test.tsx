import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RetentionConfigPanel } from "./RetentionConfigPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <RetentionConfigPanel />
    </AuthProvider>,
  );
}

const defaultConfig = { tenantId: "t1", alertRetentionMonths: 18, incidentRetentionMonths: 18, configured: false };
const savedConfig = { tenantId: "t1", alertRetentionMonths: 6, incidentRetentionMonths: 36, configured: true, updatedAt: "2026-01-01T00:00:00Z" };

describe("RetentionConfigPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("pre-fills both fields with the 18-month default and shows the 'using default' badge when unconfigured", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(defaultConfig)));
    renderPanel();

    expect(await screen.findByLabelText(/Alert retention/)).toHaveValue(18);
    expect(screen.getByLabelText(/Incident retention/)).toHaveValue(18);
    expect(screen.getByText("Using default (18 months)")).toBeInTheDocument();
  });

  it("shows the saved values and no 'using default' badge once configured", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(savedConfig)));
    renderPanel();

    expect(await screen.findByLabelText(/Alert retention/)).toHaveValue(6);
    expect(screen.getByLabelText(/Incident retention/)).toHaveValue(36);
    expect(screen.queryByText("Using default (18 months)")).not.toBeInTheDocument();
  });

  it("saving the form PUTs both values and shows a success message", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(defaultConfig));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const alertInput = await screen.findByLabelText(/Alert retention/);
    await userEvent.clear(alertInput);
    await userEvent.type(alertInput, "12");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/retention",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ alertRetentionMonths: 12, incidentRetentionMonths: 18 }),
        }),
      ),
    );
    expect(await screen.findByText("Retention settings saved.")).toBeInTheDocument();
  });

  it("a fetch error surfaces the error banner", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "internal error" }, 500)));
    renderPanel();

    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });
});
