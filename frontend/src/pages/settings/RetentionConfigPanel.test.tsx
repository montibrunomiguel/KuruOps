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

  it("raising a value saves immediately without any confirmation step", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(defaultConfig));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const alertInput = await screen.findByLabelText(/Alert retention/);
    await userEvent.clear(alertInput);
    await userEvent.type(alertInput, "24");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/retention",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ alertRetentionMonths: 24, incidentRetentionMonths: 18 }),
        }),
      ),
    );
    expect(await screen.findByText("Retention settings saved.")).toBeInTheDocument();
  });

  it("saving an unchanged value does not trigger the lower-value confirmation", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(savedConfig));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await screen.findByLabelText(/Alert retention/);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/retention",
        expect.objectContaining({ method: "PUT" }),
      ),
    );
    expect(screen.queryByRole("button", { name: "Save anyway" })).not.toBeInTheDocument();
  });

  it("lowering a value shows the confirm banner instead of saving immediately", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(savedConfig));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const alertInput = await screen.findByLabelText(/Alert retention/);
    await userEvent.clear(alertInput);
    await userEvent.type(alertInput, "3");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(
      await screen.findByText(
        "This lowers a retention period below its current value -- any closed alert or incident already older than the new value will be permanently deleted on the next sweep. This cannot be undone.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save anyway" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/retention", expect.objectContaining({ method: "PUT" }));
  });

  it("clicking Cancel during confirmation reverts to the normal Save button without submitting", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(savedConfig));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const alertInput = await screen.findByLabelText(/Alert retention/);
    await userEvent.clear(alertInput);
    await userEvent.type(alertInput, "3");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("button", { name: "Save anyway" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save anyway" })).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/retention", expect.objectContaining({ method: "PUT" }));
  });

  it("editing a field while confirming cancels the pending confirmation", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(savedConfig)));
    renderPanel();

    const alertInput = await screen.findByLabelText(/Alert retention/);
    await userEvent.clear(alertInput);
    await userEvent.type(alertInput, "3");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("button", { name: "Save anyway" })).toBeInTheDocument();

    await userEvent.type(alertInput, "0");

    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save anyway" })).not.toBeInTheDocument();
  });

  it("clicking Save anyway submits the lowered value", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(savedConfig));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const alertInput = await screen.findByLabelText(/Alert retention/);
    await userEvent.clear(alertInput);
    await userEvent.type(alertInput, "3");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await userEvent.click(await screen.findByRole("button", { name: "Save anyway" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/retention",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ alertRetentionMonths: 3, incidentRetentionMonths: 36 }),
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
