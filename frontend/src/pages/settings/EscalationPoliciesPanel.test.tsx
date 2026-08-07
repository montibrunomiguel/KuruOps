import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { EscalationPoliciesPanel } from "./EscalationPoliciesPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <EscalationPoliciesPanel />
    </AuthProvider>,
  );
}

const configuredPolicy = {
  id: "p1", tenantId: "t1", severity: "critical", unacknowledgedAfterMinutes: 15, channelType: "pagerduty",
  createdAt: "2024-01-01", updatedAt: "2024-01-01",
};

describe("EscalationPoliciesPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders every severity as not-configured when nothing is set up", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderPanel();

    expect(await screen.findAllByText("Not configured")).toHaveLength(5);
  });

  it("an already-configured severity shows its saved values", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([configuredPolicy])));
    renderPanel();

    expect(await screen.findByLabelText("Minutes unacknowledged")).toHaveValue(15);
    expect(screen.getByLabelText("Channel")).toHaveValue("pagerduty");
    expect(screen.queryAllByText("Not configured")).toHaveLength(4);
  });

  it("clicking Configure reveals the form, and Save is disabled until a destination is entered", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderPanel();

    const configureButtons = await screen.findAllByRole("button", { name: "Configure" });
    await userEvent.click(configureButtons[0]);

    expect(screen.getByLabelText("Minutes unacknowledged")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("saving a new policy PUTs severity/minutes/channel/destination", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(configuredPolicy));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const configureButtons = await screen.findAllByRole("button", { name: "Configure" });
    await userEvent.click(configureButtons[0]); // Critical is first

    await userEvent.type(screen.getByLabelText("Destination"), "R0UTING-KEY");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/escalation-policies",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({
            severity: "critical", unacknowledgedAfterMinutes: 15, channelType: "webhook", destination: "R0UTING-KEY",
          }),
        }),
      ),
    );
  });

  it("Remove requires an inline confirm click before deleting", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/escalation-policies")) return Promise.resolve(jsonResponse([configuredPolicy]));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await screen.findByLabelText("Minutes unacknowledged");
    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/escalation-policies/p1", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/escalation-policies/p1",
        expect.objectContaining({ method: "DELETE" }),
      ),
    );
  });

  it("shows the server's error message on a failed load", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "internal error" }, 500)));
    renderPanel();

    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });
});
