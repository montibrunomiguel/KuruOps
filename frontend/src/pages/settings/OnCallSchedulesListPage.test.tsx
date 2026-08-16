import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { OnCallSchedulesListPage } from "./OnCallSchedulesListPage";
import { AuthProvider } from "../../auth/AuthContext";

function wrapper({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter>
      <AuthProvider>{children}</AuthProvider>
    </MemoryRouter>
  );
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

const SCHEDULES = [
  { id: "sched1", tenantId: "t1", name: "Primary On-Call", isDefault: true, timezone: "UTC", participants: [{ userId: "u1", userName: "Alice" }], handoverAt: "2026-01-01T09:00:00Z", periodDays: 7, concurrentShifts: 1, workingHoursMode: "all_day", workingHours: [], overrides: [], createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" },
  { id: "sched2", tenantId: "t1", name: "Team B", isDefault: false, timezone: "UTC", participants: [], handoverAt: "2026-01-01T09:00:00Z", periodDays: 7, concurrentShifts: 1, workingHoursMode: "all_day", workingHours: [], overrides: [], createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" },
];

describe("OnCallSchedulesListPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders every fetched schedule, with a Default badge only on the default one", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(SCHEDULES)));
    render(<OnCallSchedulesListPage />, { wrapper });

    expect(await screen.findByText("Primary On-Call")).toBeInTheDocument();
    expect(screen.getByText("Team B")).toBeInTheDocument();
    expect(screen.getByText("Default")).toBeInTheDocument();
    expect(screen.getByText("1 responder")).toBeInTheDocument();
    expect(screen.getByText("0 responders")).toBeInTheDocument();
  });

  it("shows the empty state when there are no schedules", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    render(<OnCallSchedulesListPage />, { wrapper });

    expect(await screen.findByText("No on-call schedules yet.")).toBeInTheDocument();
  });

  it("only the non-default schedule offers a Set as default action", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(SCHEDULES)));
    render(<OnCallSchedulesListPage />, { wrapper });
    await screen.findByText("Team B");

    expect(screen.getAllByRole("button", { name: "Set as default" })).toHaveLength(1);
  });

  it("clicking Set as default posts to the schedule's default endpoint and reloads", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/v1/settings/on-call-schedules/sched2/default" && init?.method === "POST") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.resolve(jsonResponse(SCHEDULES));
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<OnCallSchedulesListPage />, { wrapper });
    await screen.findByText("Team B");

    await userEvent.click(screen.getByRole("button", { name: "Set as default" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/on-call-schedules/sched2/default",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    // reload() re-fetches the list -- at least one call after the POST.
    await waitFor(() => expect(fetchMock.mock.calls.filter((c) => c[0] === "/api/v1/settings/on-call-schedules").length).toBeGreaterThan(1));
  });
});
