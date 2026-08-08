import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
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

const configuredWebhookPolicy = {
  id: "p2", tenantId: "t1", severity: "high", unacknowledgedAfterMinutes: 30, channelType: "webhook",
  webhookPayloadTemplate: '{"text": "{{title}}"}', createdAt: "2024-01-01", updatedAt: "2024-01-01",
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

  it("an already-configured severity shows its saved channel and minutes in the overview row", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([configuredPolicy])));
    renderPanel();

    expect(await screen.findByText("Configured")).toBeInTheDocument();
    expect(screen.getByText("PagerDuty")).toBeInTheDocument();
    expect(screen.getByText("15")).toBeInTheDocument();
    expect(screen.queryAllByText("Not configured")).toHaveLength(4);
  });

  it("clicking Configure expands the edit form, and Save is disabled until a destination is entered", async () => {
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

    // Destination URL is the default channel (webhook)'s label.
    await userEvent.type(screen.getByLabelText("Destination URL"), "R0UTING-KEY");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/escalation-policies",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({
            severity: "critical", unacknowledgedAfterMinutes: 15, channelType: "webhook",
            destination: "R0UTING-KEY", webhookPayloadTemplate: "",
          }),
        }),
      ),
    );
  });

  it("choosing webhook reveals the custom payload field and includes it on save", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(configuredWebhookPolicy));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const configureButtons = await screen.findAllByRole("button", { name: "Configure" });
    await userEvent.click(configureButtons[1]); // High

    await userEvent.type(screen.getByLabelText("Destination URL"), "https://hook.example/x");
    const payloadField = screen.getByLabelText("Custom payload (optional)");
    // userEvent.type() parses "{"/"}" as special-key syntax -- fireEvent
    // sidesteps that for a literal payload template containing braces.
    fireEvent.change(payloadField, { target: { value: '{"text": "{{title}}"}' } });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/escalation-policies",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({
            severity: "high", unacknowledgedAfterMinutes: 15, channelType: "webhook",
            destination: "https://hook.example/x", webhookPayloadTemplate: '{"text": "{{title}}"}',
          }),
        }),
      ),
    );
  });

  it("Send test posts the severity and shows a success message", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST" && url.includes("/test")) return Promise.resolve(jsonResponse({ ok: true }));
      if (url.includes("/escalation-policies")) return Promise.resolve(jsonResponse([configuredPolicy]));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Send test" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/escalation-policies/test",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ severity: "critical" }) }),
      ),
    );
    expect(await screen.findByText("Test notification sent.")).toBeInTheDocument();
  });

  it("Remove requires an inline confirm click before deleting", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/escalation-policies")) return Promise.resolve(jsonResponse([configuredPolicy]));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await screen.findByText("Configured");
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
