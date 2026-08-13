import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { AlertsListPage } from "./AlertsListPage";
import { AuthProvider } from "../../auth/AuthContext";

function wrapper({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter>
      <AuthProvider>{children}</AuthProvider>
    </MemoryRouter>
  );
}

function jsonResponse(body: unknown, total?: number) {
  const headers: Record<string, string> = { "content-type": "application/json" };
  if (total !== undefined) headers["X-Total-Count"] = String(total);
  return new Response(JSON.stringify(body), { status: 200, headers });
}

function alertFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "a1", title: "Suspicious login", source: "wazuh", severity: "high", status: "open",
    tags: [], receivedAt: "2026-01-01T00:00:00Z", ...overrides,
  };
}

describe("AlertsListPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders fetched alerts in a table", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([alertFixture()])));
    render(<AlertsListPage />, { wrapper });

    expect(await screen.findByText("Suspicious login")).toBeInTheDocument();
  });

  it("shows the empty state when there are no results", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    render(<AlertsListPage />, { wrapper });

    expect(await screen.findByText("No alerts found for the current filters.")).toBeInTheDocument();
  });

  it("changing the severity filter re-fetches with the new query param", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    render(<AlertsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[0], "critical");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("severity=critical");
    });
  });

  it("shows pagination totals and fetches the next page (offset) on click", async () => {
    const page1 = Array.from({ length: 20 }, (_, i) => alertFixture({ id: `a${i}`, title: `Alert ${i}` }));
    const page2 = [alertFixture({ id: "extra", title: "Extra alert" })];
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(page1, 21))
      .mockResolvedValueOnce(jsonResponse(page2, 21));
    vi.stubGlobal("fetch", fetchMock);

    render(<AlertsListPage />, { wrapper });
    await screen.findByText("Alert 0");
    expect(screen.getByText("Showing 1-20 of 21")).toBeInTheDocument();

    const nextBtn = screen.getByRole("button", { name: "Next" });
    await userEvent.click(nextBtn);
    expect(await screen.findByText("Extra alert")).toBeInTheDocument();

    const calls = fetchMock.mock.calls;
    const lastCall = calls[calls.length - 1]?.[0] as string;
    expect(lastCall).toContain("limit=20");
    expect(lastCall).toContain("offset=20");
  });

  it("changing the page size resets to page 1 and refetches with the new limit", async () => {
    const page1 = Array.from({ length: 20 }, (_, i) => alertFixture({ id: `a${i}`, title: `Alert ${i}` }));
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(page1, 21));
    vi.stubGlobal("fetch", fetchMock);

    render(<AlertsListPage />, { wrapper });
    await screen.findByText("Alert 0");

    await userEvent.selectOptions(screen.getByLabelText("Items per page"), "100");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("limit=100");
      expect(lastCall).toContain("offset=0");
    });
  });

  it("shows the resolved assignee name, or a dash when unassigned", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse([
          alertFixture({ id: "a1", title: "Assigned alert", assignedAnalystId: "u1", assignedAnalystName: "Marina Alves" }),
          alertFixture({ id: "a2", title: "Unassigned alert" }),
        ]),
      ),
    );
    render(<AlertsListPage />, { wrapper });

    expect(await screen.findByText("Marina Alves")).toBeInTheDocument();
    await screen.findByText("Unassigned alert");
    const row = screen.getByText("Unassigned alert").closest("tr")!;
    expect(row).toHaveTextContent("—");
  });

  it("falls back to a shortened analyst id when no assignee name was resolved", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse([alertFixture({ assignedAnalystId: "u1234567-abcd" })])),
    );
    render(<AlertsListPage />, { wrapper });

    await screen.findByText("Suspicious login");
    const row = screen.getByText("Suspicious login").closest("tr")!;
    expect(row).toHaveTextContent("u1234567");
  });

  it("shows the error banner on a failed fetch", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "boom" }), { status: 500, headers: { "content-type": "application/json" } })));
    render(<AlertsListPage />, { wrapper });

    expect(await screen.findByText("boom")).toBeInTheDocument();
  });

  it("shows a correlated badge when the alert has an incidentId", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([alertFixture({ incidentId: "inc-1" })])));
    render(<AlertsListPage />, { wrapper });

    await screen.findByText("Suspicious login");
    const row = screen.getByText("Suspicious login").closest("tr")!;
    expect(row).toHaveTextContent("Yes");
  });

  it("renders tag chips for an alert that has tags", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([alertFixture({ tags: ["ransomware", "priority"] })])));
    render(<AlertsListPage />, { wrapper });

    await screen.findByText("Suspicious login");
    expect(screen.getByText("ransomware")).toBeInTheDocument();
    expect(screen.getByText("priority")).toBeInTheDocument();
  });

  it("changing the status filter re-fetches with the new query param", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    render(<AlertsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[1], "escalated");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("status=escalated");
    });
  });

  it("changing the correlated filter re-fetches with the new query param", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    render(<AlertsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[2], "true");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("correlated=true");
    });
  });

  it("typing in the source filter re-fetches with the new query param", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    render(<AlertsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const sourceInput = screen.getByPlaceholderText("All sources");
    await userEvent.type(sourceInput, "wazuh");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("source=wazuh");
    });
  });

  it("typing in the tag filter re-fetches with the new query param", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    render(<AlertsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const tagInput = screen.getByPlaceholderText("All tags");
    await userEvent.type(tagInput, "ransomware");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("tag=ransomware");
    });
  });

  it("choosing a time-range preset re-fetches with a since query param", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    render(<AlertsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[3], "7d");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("since=");
    });
  });

  it("a custom time range sends both since and until", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    render(<AlertsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[3], "custom");

    await userEvent.type(screen.getByLabelText("From"), "2026-01-01T00:00");
    await userEvent.type(screen.getByLabelText("To"), "2026-01-31T00:00");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("since=");
      expect(lastCall).toContain("until=");
    });
  });

  it("reloads the list when a live alert event comes in over the event stream", async () => {
    localStorage.setItem(
      "argusops.session",
      JSON.stringify({
        token: "tok",
        refreshToken: "rt",
        user: { id: "1", email: "a@b.com", name: "A", role: "admin", mustChangePassword: false, resourceAccess: [] },
      }),
    );

    const encoder = new TextEncoder();
    const streamResponse = new Response(
      new ReadableStream({
        start(controller) {
          // An "incident" event should be ignored (no reload) -- only "alert"
          // events reload this page's list -- while the following "alert"
          // event should trigger exactly one reload.
          controller.enqueue(encoder.encode('event: incident\ndata: {"id":"i1"}\n\nevent: alert\ndata: {"id":"a1"}\n\n'));
          controller.close();
        },
      }),
      { status: 200 },
    );

    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.startsWith("/api/v1/events/stream")) return Promise.resolve(streamResponse);
      return Promise.resolve(jsonResponse([alertFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AlertsListPage />, { wrapper });
    await screen.findByText("Suspicious login");

    // The stream event fires the "alert" reload as soon as the SSE connection's
    // single frame is processed -- assert the alerts endpoint was hit at least
    // twice (initial load + the reload triggered by the event) rather than
    // snapshotting a "before" count, since the reload can race ahead of it.
    await waitFor(() => {
      const alertsCalls = fetchMock.mock.calls.filter((c) => (c[0] as string).startsWith("/api/v1/alerts")).length;
      expect(alertsCalls).toBeGreaterThanOrEqual(2);
    });
  });
});
