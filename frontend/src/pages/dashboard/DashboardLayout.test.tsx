import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { DashboardLayout } from "./DashboardLayout";
import { AuthProvider } from "../../auth/AuthContext";

function sessionWith(resourceAccess: string[]) {
  localStorage.setItem(
    "argusops.session",
    JSON.stringify({
      token: "tok",
      user: { id: "1", email: "a@b.com", name: "A", role: "analyst", mustChangePassword: false, resourceAccess },
    }),
  );
}

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

function renderDashboard(initialPath: string) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <AuthProvider>
        <Routes>
          <Route path="/dashboard/*" element={<DashboardLayout />} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("DashboardLayout", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
  });

  it("shows only the tabs the user has capability for", async () => {
    sessionWith(["alerts"]);
    renderDashboard("/dashboard/alerts");

    await waitFor(() => expect(screen.getByText("Alerts")).toBeInTheDocument());
    expect(screen.queryByText("Incidents")).not.toBeInTheDocument();
    expect(screen.queryByText("Follow-up")).not.toBeInTheDocument();
  });

  it("redirects the bare /dashboard index to the first available tab", async () => {
    sessionWith(["incidents"]);
    renderDashboard("/dashboard");

    await waitFor(() => expect(screen.getByText("No incidents yet.")).toBeInTheDocument());
  });

  it("shows a no-access message when the user has none of the three capabilities", async () => {
    sessionWith([]);
    renderDashboard("/dashboard");

    await waitFor(() =>
      expect(screen.getByText("Your account doesn't have access to any dashboard section.")).toBeInTheDocument(),
    );
  });
});

describe("AlertsTabPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders the stats-derived KPI cards", async () => {
    sessionWith(["alerts"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/dashboard/stats")) {
          return Promise.resolve(
            jsonResponse({
              openAlerts: 5,
              criticalAlerts: 3,
              highAlerts: 7,
              activeIncidents: 4,
              slaBreachedCount: 2,
              alertTrend: [],
              alertsBySeverity: { critical: 3, high: 2 },
              alertStatusDistribution: { open: 3, closed: 2 },
              incidentsByPriority: {},
              incidentsByPhase: {},
            }),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderDashboard("/dashboard/alerts");
    expect(await screen.findByText("Open Alerts")).toBeInTheDocument();
    expect(screen.getAllByText("5").length).toBeGreaterThan(0);
    expect(screen.getByText("High Severity")).toBeInTheDocument();
    expect(screen.getAllByText("7").length).toBeGreaterThan(0);
    expect(screen.getByText("Alerts by Severity")).toBeInTheDocument();
    expect(screen.queryByText("Active Incidents")).not.toBeInTheDocument();
  });

  it("shows the empty state when there is no recent activity", async () => {
    sessionWith(["alerts"]);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));

    renderDashboard("/dashboard/alerts");
    expect(await screen.findByText("No recent activity.")).toBeInTheDocument();
  });

  it("fetches recent activity scoped to kind=alert", async () => {
    sessionWith(["alerts"]);
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);

    renderDashboard("/dashboard/alerts");
    await waitFor(() => expect(screen.getByText("Open Alerts")).toBeInTheDocument());

    const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
    expect(calls.some((u) => u.includes("/dashboard/activity?kind=alert"))).toBe(true);
  });

  it("changing the severity filter re-fetches stats with the alertSeverity query param", async () => {
    sessionWith(["alerts"]);
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/dashboard/stats")) {
        return Promise.resolve(
          jsonResponse({
            openAlerts: 0, criticalAlerts: 0, activeIncidents: 0, slaBreachedCount: 0,
            alertTrend: [], alertsBySeverity: {}, alertStatusDistribution: {}, incidentsByPriority: {}, incidentsByPhase: {},
          }),
        );
      }
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);

    renderDashboard("/dashboard/alerts");
    await waitFor(() => expect(screen.getByText("Open Alerts")).toBeInTheDocument());

    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[0], "critical");

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
      expect(calls.some((u) => u.includes("/dashboard/stats?alertSeverity=critical"))).toBe(true);
    });
  });
});

describe("FollowupTabPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders the triage queue from the follow-up view's alerts", async () => {
    sessionWith(["followup"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/dashboard/followup")) {
          return Promise.resolve(
            jsonResponse({
              incidents: [],
              alerts: [{ id: "a1", title: "Escalated alert", source: "wazuh", status: "escalated", severity: "high", receivedAt: "2026-01-01T00:00:00Z" }],
            }),
          );
        }
        return Promise.resolve(jsonResponse({}));
      }),
    );

    renderDashboard("/dashboard/followup");
    expect(await screen.findByText("Escalated alert")).toBeInTheDocument();
  });

  it("shows the empty state when nothing needs follow-up", async () => {
    sessionWith(["followup"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ incidents: [], alerts: [] })),
    );

    renderDashboard("/dashboard/followup");
    expect(await screen.findByText("Nothing pending follow-up.")).toBeInTheDocument();
  });
});
