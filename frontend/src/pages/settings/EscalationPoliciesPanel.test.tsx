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

const SCHEDULES = [
  { id: "sched1", tenantId: "t1", name: "Primary", isDefault: true, timezone: "UTC", participants: [], handoverAt: "2026-01-01T09:00:00Z", periodDays: 7, concurrentShifts: 1, workingHoursMode: "all_day", workingHours: [], overrides: [], createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" },
  { id: "sched2", tenantId: "t1", name: "DBA Team", isDefault: false, timezone: "UTC", participants: [], handoverAt: "2026-01-01T09:00:00Z", periodDays: 7, concurrentShifts: 1, workingHoursMode: "all_day", workingHours: [], overrides: [], createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" },
];

const configuredPolicy = {
  id: "p1", tenantId: "t1", severity: "critical",
  steps: [
    { id: "s1", policyId: "p1", position: 0, scheduleId: "sched1", scheduleName: "Primary", delayMinutes: 15, channelType: "pagerduty" },
  ],
  createdAt: "2024-01-01", updatedAt: "2024-01-01",
};

function fetchDispatcher(policies: unknown[], handlers: Partial<Record<string, (url: string, init?: RequestInit) => Promise<Response>>> = {}) {
  return vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (url.includes("/on-call-schedules")) return Promise.resolve(jsonResponse(SCHEDULES));
    if (url.includes("/escalation-policies/test")) return (handlers.test ?? (() => Promise.resolve(jsonResponse({ ok: true }))))(url, init);
    if (url.includes("/escalation-policies") && init?.method === "PUT") return (handlers.put ?? (() => Promise.resolve(jsonResponse(configuredPolicy))))(url, init);
    if (url.includes("/escalation-policies") && init?.method === "DELETE") return (handlers.del ?? (() => Promise.resolve(new Response(null, { status: 204 }))))(url, init);
    if (url.includes("/escalation-policies")) return (handlers.list ?? (() => Promise.resolve(jsonResponse(policies))))(url, init);
    return Promise.resolve(jsonResponse({}));
  });
}

describe("EscalationPoliciesPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows the no-schedules empty state instead of the table when there are no On-Call Schedules", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((url: string) => {
      if (url.includes("/on-call-schedules")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse([]));
    }));
    renderPanel();

    expect(await screen.findByText(/No On-Call Schedules configured yet/)).toBeInTheDocument();
  });

  it("renders every severity as not-configured when nothing is set up", async () => {
    vi.stubGlobal("fetch", fetchDispatcher([]));
    renderPanel();

    expect(await screen.findAllByText("Not configured")).toHaveLength(5);
  });

  it("an already-configured severity shows its chain's step summary in the overview row", async () => {
    vi.stubGlobal("fetch", fetchDispatcher([configuredPolicy]));
    renderPanel();

    expect(await screen.findByText("Configured")).toBeInTheDocument();
    expect(screen.getByText("Primary")).toBeInTheDocument();
    expect(screen.getByText("15min -- PagerDuty")).toBeInTheDocument();
    expect(screen.queryAllByText("Not configured")).toHaveLength(4);
  });

  it("clicking Configure expands the edit form with one empty step, and Save is disabled until a destination is entered", async () => {
    vi.stubGlobal("fetch", fetchDispatcher([]));
    renderPanel();

    const configureButtons = await screen.findAllByRole("button", { name: "Configure" });
    await userEvent.click(configureButtons[0]);

    expect(screen.getByLabelText("On-Call Schedule")).toBeInTheDocument();
    expect(screen.getByLabelText("Delay (minutes)")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("saving a new single-step chain PUTs severity + the one step's fields", async () => {
    const fetchMock = fetchDispatcher([]);
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
            severity: "critical",
            steps: [{ scheduleId: "sched1", delayMinutes: 15, channelType: "webhook", destination: "R0UTING-KEY", webhookPayloadTemplate: "" }],
          }),
        }),
      ),
    );
  });

  it("choosing webhook reveals the custom payload field (with analyst placeholders) and includes it on save", async () => {
    const fetchMock = fetchDispatcher([]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const configureButtons = await screen.findAllByRole("button", { name: "Configure" });
    await userEvent.click(configureButtons[1]); // High

    await userEvent.type(screen.getByLabelText("Destination URL"), "https://hook.example/x");
    expect(screen.getByText(/analystName/)).toBeInTheDocument();
    expect(screen.getByText(/analystPhone/)).toBeInTheDocument();
    const payloadField = screen.getByLabelText("Custom payload (optional)");
    // userEvent.type() parses "{"/"}" as special-key syntax -- fireEvent
    // sidesteps that for a literal payload template containing braces.
    fireEvent.change(payloadField, { target: { value: '{"text": "{{title}} {{analystName}}"}' } });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/escalation-policies",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({
            severity: "high",
            steps: [{ scheduleId: "sched1", delayMinutes: 15, channelType: "webhook", destination: "https://hook.example/x", webhookPayloadTemplate: '{"text": "{{title}} {{analystName}}"}' }],
          }),
        }),
      ),
    );
  });

  it("+ Add step appends a second step, and reordering with the move buttons changes save order", async () => {
    const fetchMock = fetchDispatcher([]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const configureButtons = await screen.findAllByRole("button", { name: "Configure" });
    await userEvent.click(configureButtons[0]);

    await userEvent.click(screen.getByRole("button", { name: "+ Add step" }));
    const scheduleSelects = screen.getAllByLabelText("On-Call Schedule");
    expect(scheduleSelects).toHaveLength(2);
    await userEvent.selectOptions(scheduleSelects[1], "sched2");

    const destinations = screen.getAllByLabelText("Destination URL");
    await userEvent.type(destinations[0], "https://hook.example/1");
    await userEvent.type(destinations[1], "https://hook.example/2");

    // Move the second step up -- it should become position 0 on save.
    const moveUpButtons = screen.getAllByRole("button", { name: "Move up" });
    await userEvent.click(moveUpButtons[1]);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const putCall = fetchMock.mock.calls.find((c) => c[0] === "/api/v1/settings/escalation-policies" && (c[1] as RequestInit)?.method === "PUT");
      expect(putCall).toBeDefined();
      const body = JSON.parse((putCall![1] as RequestInit).body as string);
      expect(body.steps).toHaveLength(2);
      expect(body.steps[0].scheduleId).toBe("sched2");
      expect(body.steps[1].scheduleId).toBe("sched1");
    });
  });

  it("Send test on a saved step posts the severity + step position and shows a success message", async () => {
    const fetchMock = fetchDispatcher([configuredPolicy]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Send test" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/escalation-policies/test",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ severity: "critical", stepPosition: 0 }) }),
      ),
    );
    expect(await screen.findByText("Test notification sent.")).toBeInTheDocument();
  });

  it("Remove requires an inline confirm click before deleting", async () => {
    const fetchMock = fetchDispatcher([configuredPolicy]);
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await screen.findByText("Configured");
    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(fetchMock.mock.calls.some((c) => (c[1] as RequestInit | undefined)?.method === "DELETE")).toBe(false);

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/escalation-policies/p1",
        expect.objectContaining({ method: "DELETE" }),
      ),
    );
  });

  it("shows the server's error message on a failed load", async () => {
    vi.stubGlobal("fetch", fetchDispatcher([], { list: () => Promise.resolve(jsonResponse({ error: "internal error" }, 500)) }));
    renderPanel();

    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });
});
