import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuthProvider } from "../../../auth/AuthContext";
import { LinkedAlertsPanel } from "./LinkedAlertsPanel";
import type { Alert } from "../../../types/alerts";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function alertFixture(overrides: Partial<Alert> = {}): Alert {
  return {
    id: "linked-1",
    tenantId: "t1",
    title: "Correlated brute force",
    source: "wazuh",
    severity: "high",
    status: "open",
    receivedAt: "2026-01-01T00:00:00Z",
    tags: [],
    metadata: {},
    duplicateCount: 0,
    ...overrides,
  } as Alert;
}

function routeFetch(linked: Alert[] = [alertFixture()], candidates: Alert[] = []) {
  return vi.fn().mockImplementation((url: string) => {
    if (url.includes("/alerts/main-alert/alerts")) return Promise.resolve(jsonResponse(linked));
    if (url.includes("/alerts?limit=50")) return Promise.resolve(jsonResponse(candidates));
    return Promise.resolve(jsonResponse({}));
  });
}

function renderPanel(fetchMock: ReturnType<typeof vi.fn>) {
  vi.stubGlobal("fetch", fetchMock);
  return render(
    <AuthProvider>
      <LinkedAlertsPanel alertId="main-alert" />
    </AuthProvider>,
  );
}

describe("LinkedAlertsPanel", () => {
  it("renders each linked alert as a chip with an unlink button", async () => {
    renderPanel(routeFetch());
    expect(await screen.findByText(/Correlated brute force/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Unlink alert/ })).toBeInTheDocument();
  });

  it("clicking the unlink button asks for confirmation instead of unlinking immediately", async () => {
    const fetchMock = routeFetch();
    renderPanel(fetchMock);
    await screen.findByText(/Correlated brute force/);

    await userEvent.click(screen.getByRole("button", { name: /Unlink alert/ }));

    // The DELETE call must not have fired from the first click alone.
    expect(fetchMock).not.toHaveBeenCalledWith(expect.stringContaining("/alerts/main-alert/alerts/linked-1"), expect.objectContaining({ method: "DELETE" }));
    const confirmButton = screen.getByRole("button", { name: "Confirm" });
    expect(confirmButton).toHaveAttribute("title", "Unlink linked-1?");
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  });

  it("confirming the unlink sends the DELETE and reloads", async () => {
    const fetchMock = routeFetch();
    renderPanel(fetchMock);
    await screen.findByText(/Correlated brute force/);

    await userEvent.click(screen.getByRole("button", { name: /Unlink alert/ }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/alerts/main-alert/alerts/linked-1"),
        expect.objectContaining({ method: "DELETE" }),
      ),
    );
  });

  it("cancelling the unlink confirmation leaves the link untouched", async () => {
    const fetchMock = routeFetch();
    renderPanel(fetchMock);
    await screen.findByText(/Correlated brute force/);

    await userEvent.click(screen.getByRole("button", { name: /Unlink alert/ }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Unlink alert/ })).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith(expect.stringContaining("/alerts/main-alert/alerts/linked-1"), expect.objectContaining({ method: "DELETE" }));
  });

  it("shows the empty state when there are no linked alerts", async () => {
    renderPanel(routeFetch([]));
    expect(await screen.findByText("No linked alerts yet.")).toBeInTheDocument();
  });

  it("links a candidate alert found via the search picker", async () => {
    const candidate = alertFixture({ id: "candidate-1", title: "Suspicious login from new device" });
    const fetchMock = routeFetch([], [candidate]);
    renderPanel(fetchMock);
    await screen.findByText("No linked alerts yet.");

    await userEvent.type(screen.getByPlaceholderText(/Search alerts/), "suspicious");
    await userEvent.click(await screen.findByText(/Suspicious login from new device/));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/alerts/main-alert/alerts/candidate-1"),
        expect.objectContaining({ method: "PUT" }),
      ),
    );
  });
});
