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

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
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

  it("shows a Load More button when a full page comes back, and loads the next page on click", async () => {
    const fullPage = Array.from({ length: 50 }, (_, i) => alertFixture({ id: `a${i}`, title: `Alert ${i}` }));
    const fetchMock = vi.fn().mockResolvedValueOnce(jsonResponse(fullPage)).mockResolvedValueOnce(jsonResponse([alertFixture({ id: "extra", title: "Extra alert" })]));
    vi.stubGlobal("fetch", fetchMock);

    render(<AlertsListPage />, { wrapper });
    const loadMoreBtn = await screen.findByRole("button", { name: "Load more" });

    await userEvent.click(loadMoreBtn);
    expect(await screen.findByText("Extra alert")).toBeInTheDocument();
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

  it("shows the error banner on a failed fetch", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "boom" }), { status: 500, headers: { "content-type": "application/json" } })));
    render(<AlertsListPage />, { wrapper });

    expect(await screen.findByText("boom")).toBeInTheDocument();
  });
});
