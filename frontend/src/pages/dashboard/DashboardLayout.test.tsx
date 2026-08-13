import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
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
    expect((await screen.findAllByText("5")).length).toBeGreaterThan(0);
    expect(screen.getByText("High Severity")).toBeInTheDocument();
    expect((await screen.findAllByText("7")).length).toBeGreaterThan(0);
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

  it("changing the time range re-fetches stats and activity with a since query param", async () => {
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

    const timeRangeSelect = screen.getByLabelText("Time range");
    await userEvent.selectOptions(timeRangeSelect, "7d");

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
      expect(calls.some((u) => u.startsWith("/api/v1/dashboard/stats?") && u.includes("since="))).toBe(true);
      expect(calls.some((u) => u.startsWith("/api/v1/dashboard/activity?") && u.includes("since="))).toBe(true);
    });
  });

  it("selecting a custom range reveals from/to pickers and re-fetches with since and until", async () => {
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

    await userEvent.selectOptions(screen.getByLabelText("Time range"), "custom");
    const fromInput = await screen.findByLabelText("From");
    const toInput = screen.getByLabelText("To");

    await userEvent.type(fromInput, "2026-01-01T09:00");
    await userEvent.type(toInput, "2026-01-02T18:30");

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
      expect(calls.some((u) => u.startsWith("/api/v1/dashboard/stats?") && u.includes("since=") && u.includes("until="))).toBe(true);
    });
  });

  it("renders the Alerts by Analyst breakdown, unassigned bucket included", async () => {
    sessionWith(["alerts"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }]));
        if (url.includes("/dashboard/stats")) {
          return Promise.resolve(
            jsonResponse({
              openAlerts: 0, criticalAlerts: 0, activeIncidents: 0, slaBreachedCount: 0,
              alertTrend: [], alertsBySeverity: {}, alertStatusDistribution: {}, incidentsByPriority: {}, incidentsByPhase: {},
              alertsByAnalyst: [{ id: "u1", name: "Marina Alves", count: 3 }, { name: "", count: 1 }],
            }),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderDashboard("/dashboard/alerts");
    await waitFor(() => expect(screen.getByText("Alerts by Analyst")).toBeInTheDocument());
    const panel = within(screen.getByText("Alerts by Analyst").closest(".panel") as HTMLElement);
    expect(panel.getByText("Marina Alves")).toBeInTheDocument();
    expect(panel.getByText("Unassigned")).toBeInTheDocument();
  });

  it("changing the analyst filter re-fetches stats with the assignedAnalystId query param", async () => {
    sessionWith(["alerts"]);
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }]));
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

    const analystSelect = await screen.findByText("Marina Alves");
    await userEvent.selectOptions(analystSelect.closest("select") as HTMLSelectElement, "u1");

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
      expect(calls.some((u) => u.startsWith("/api/v1/dashboard/stats?") && u.includes("assignedAnalystId=u1"))).toBe(true);
    });
  });

  it("changing the status, source, and tag filters re-fetches stats with the new query params", async () => {
    sessionWith(["alerts"]);
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([{ id: "t1", name: "ransomware" }]));
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

    // Severity/status are now MultiSelectFilter's own <select>s (still one
    // <select> each, same "select an option" interaction as before) --
    // severity's is first, status's second.
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[1], "escalated");
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0] as string);
      expect(calls.some((u) => u.includes("alertStatus=escalated"))).toBe(true);
    });

    await userEvent.type(screen.getByPlaceholderText("All sources"), "wazuh");
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0] as string);
      expect(calls.some((u) => u.includes("alertSource=wazuh"))).toBe(true);
    });

    // Tag is now TagPicker (catalog-backed multi-select, same idiom as the
    // analyst filter above) instead of a free-text input.
    const tagOption = await screen.findByText("ransomware");
    await userEvent.selectOptions(tagOption.closest("select") as HTMLSelectElement, "ransomware");
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0] as string);
      expect(calls.some((u) => u.includes("alertTag=ransomware"))).toBe(true);
    });
  });

  it("reloads stats and activity when a live alert event comes in over the event stream, and ignores other event types", async () => {
    sessionWith(["alerts"]);

    const encoder = new TextEncoder();
    function streamResponse(frame: string) {
      return new Response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode(frame));
            controller.close();
          },
        }),
        { status: 200 },
      );
    }

    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.startsWith("/api/v1/events/stream")) {
        return Promise.resolve(streamResponse('event: incident\ndata: {"id":"i1"}\n\nevent: alert\ndata: {"id":"a1"}\n\n'));
      }
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

    await waitFor(() => {
      const statsCalls = fetchMock.mock.calls.filter((c) => (c[0] as string).includes("/dashboard/stats")).length;
      const activityCalls = fetchMock.mock.calls.filter((c) => (c[0] as string).includes("/dashboard/activity")).length;
      expect(statsCalls).toBeGreaterThanOrEqual(2);
      expect(activityCalls).toBeGreaterThanOrEqual(2);
    });
  });

  it("renders recent activity items with their icon and relative time", async () => {
    sessionWith(["alerts"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/dashboard/activity")) {
          return Promise.resolve(
            jsonResponse([
              {
                kind: "alert", contextId: "a1", contextTitle: "wazuh", eventType: "received",
                actorType: "system", data: {}, createdAt: "2026-01-01T00:00:00Z",
              },
            ]),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderDashboard("/dashboard/alerts");
    expect(await screen.findByText("New webhook alert received from wazuh")).toBeInTheDocument();
  });

  it("shows the error banner when the stats request fails", async () => {
    sessionWith(["alerts"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/dashboard/stats")) {
          return Promise.resolve(
            new Response(JSON.stringify({ error: "stats boom" }), { status: 500, headers: { "content-type": "application/json" } }),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderDashboard("/dashboard/alerts");
    expect(await screen.findByText("stats boom")).toBeInTheDocument();
  });
});

describe("IncidentsTabPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders the Incidents by Commander breakdown, no-commander bucket included", async () => {
    sessionWith(["incidents"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([{ id: "u1", name: "Diego Costa" }]));
        if (url.includes("/dashboard/stats")) {
          return Promise.resolve(
            jsonResponse({
              openAlerts: 0, criticalAlerts: 0, activeIncidents: 0, slaBreachedCount: 0,
              alertTrend: [], alertsBySeverity: {}, alertStatusDistribution: {}, incidentsByPriority: {}, incidentsByPhase: {},
              incidentsByCommander: [{ id: "u1", name: "Diego Costa", count: 2 }, { name: "", count: 1 }],
            }),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderDashboard("/dashboard/incidents");
    await waitFor(() => expect(screen.getByText("Incidents by Commander")).toBeInTheDocument());
    const panel = within(screen.getByText("Incidents by Commander").closest(".panel") as HTMLElement);
    expect(panel.getByText("Diego Costa")).toBeInTheDocument();
    expect(panel.getByText("No commander")).toBeInTheDocument();
  });

  it("renders the Incident Volume chart from stats.incidentTrend", async () => {
    sessionWith(["incidents"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([]));
        if (url.includes("/dashboard/stats")) {
          return Promise.resolve(
            jsonResponse({
              openAlerts: 0, criticalAlerts: 0, activeIncidents: 0, slaBreachedCount: 0,
              alertTrend: [], alertsBySeverity: {}, alertStatusDistribution: {}, incidentsByPriority: {}, incidentsByPhase: {},
              incidentTrend: [{ day: "2026-01-01", incidentCount: 3 }, { day: "2026-01-02", incidentCount: 1 }],
            }),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderDashboard("/dashboard/incidents");
    expect(await screen.findByText("Incident Volume")).toBeInTheDocument();
  });

  it("changing the commander filter re-fetches stats with the commanderId query param", async () => {
    sessionWith(["incidents"]);
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([{ id: "u1", name: "Diego Costa" }]));
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

    renderDashboard("/dashboard/incidents");
    await waitFor(() => expect(screen.getByText("Active Incidents")).toBeInTheDocument());

    const commanderSelect = await screen.findByText("Diego Costa");
    await userEvent.selectOptions(commanderSelect.closest("select") as HTMLSelectElement, "u1");

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
      expect(calls.some((u) => u.startsWith("/api/v1/dashboard/stats?") && u.includes("commanderId=u1"))).toBe(true);
    });
  });

  it("changing the severity filter re-fetches stats and the incident list with the severity query params", async () => {
    sessionWith(["incidents"]);
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

    renderDashboard("/dashboard/incidents");
    await waitFor(() => expect(screen.getByText("Active Incidents")).toBeInTheDocument());

    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[0], "critical");

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
      expect(calls.some((u) => u.startsWith("/api/v1/dashboard/stats?") && u.includes("incidentSeverity=critical"))).toBe(true);
      expect(calls.some((u) => u.startsWith("/api/v1/incidents?") && u.includes("severity=critical"))).toBe(true);
    });
  });

  it("selecting a tag filter re-fetches stats and the incident list with the tag query params", async () => {
    sessionWith(["incidents"]);
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([{ id: "t1", name: "ransomware" }]));
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

    renderDashboard("/dashboard/incidents");
    await waitFor(() => expect(screen.getByText("Active Incidents")).toBeInTheDocument());

    const tagOption = await screen.findByText("ransomware");
    await userEvent.selectOptions(tagOption.closest("select") as HTMLSelectElement, "ransomware");

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
      expect(calls.some((u) => u.startsWith("/api/v1/dashboard/stats?") && u.includes("incidentTag=ransomware"))).toBe(true);
      expect(calls.some((u) => u.startsWith("/api/v1/incidents?") && u.includes("tag=ransomware"))).toBe(true);
    });
  });

  it("changing the time range re-fetches stats and the incident list with since/until query params", async () => {
    sessionWith(["incidents"]);
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

    renderDashboard("/dashboard/incidents");
    await waitFor(() => expect(screen.getByText("Active Incidents")).toBeInTheDocument());

    await userEvent.selectOptions(screen.getByLabelText("Time range"), "custom");
    const fromInput = await screen.findByLabelText("From");
    const toInput = screen.getByLabelText("To");
    await userEvent.type(fromInput, "2026-01-01T09:00");
    await userEvent.type(toInput, "2026-01-02T18:30");

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
      expect(calls.some((u) => u.startsWith("/api/v1/dashboard/stats?") && u.includes("since=") && u.includes("until="))).toBe(true);
      expect(calls.some((u) => u.startsWith("/api/v1/incidents?") && u.includes("since=") && u.includes("until="))).toBe(true);
    });
  });

  it("shows the error banner when the stats request fails", async () => {
    sessionWith(["incidents"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/dashboard/stats")) {
          return Promise.resolve(
            new Response(JSON.stringify({ error: "stats boom" }), { status: 500, headers: { "content-type": "application/json" } }),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderDashboard("/dashboard/incidents");
    expect(await screen.findByText("stats boom")).toBeInTheDocument();
  });

  it("shows critical SLA-breach styling and copy when slaBreachedCount is nonzero", async () => {
    sessionWith(["incidents"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/dashboard/stats")) {
          return Promise.resolve(
            jsonResponse({
              openAlerts: 0, criticalAlerts: 0, activeIncidents: 0, slaBreachedCount: 3,
              alertTrend: [], alertsBySeverity: {}, alertStatusDistribution: {}, incidentsByPriority: {}, incidentsByPhase: {},
            }),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderDashboard("/dashboard/incidents");
    expect(await screen.findByText("SLA Breached")).toBeInTheDocument();
    expect(screen.getByText("Needs attention")).toBeInTheDocument();
  });

  it("renders the recent incidents table with joined assignee names, and a dash when unassigned", async () => {
    sessionWith(["incidents"]);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.startsWith("/api/v1/incidents")) {
          return Promise.resolve(
            jsonResponse([
              {
                id: "i1", title: "Ransomware outbreak", severity: "critical", phase: "containment",
                assignees: [{ id: "u1", name: "Diego Costa" }, { id: "u2", name: "Marina Alves" }],
              },
              { id: "i2", title: "Unassigned incident", severity: "low", phase: "new", assignees: [] },
            ]),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderDashboard("/dashboard/incidents");
    await screen.findByText("Ransomware outbreak");
    const assignedRow = screen.getByText("Ransomware outbreak").closest("tr")!;
    expect(assignedRow).toHaveTextContent("Diego Costa, Marina Alves");

    const unassignedRow = screen.getByText("Unassigned incident").closest("tr")!;
    expect(unassignedRow).toHaveTextContent("—");
  });

  it("reloads stats and the incident list when a live incident event comes in over the event stream, and ignores other event types", async () => {
    sessionWith(["incidents"]);

    const encoder = new TextEncoder();
    function streamResponse(frame: string) {
      return new Response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode(frame));
            controller.close();
          },
        }),
        { status: 200 },
      );
    }

    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.startsWith("/api/v1/events/stream")) {
        return Promise.resolve(streamResponse('event: alert\ndata: {"id":"a1"}\n\nevent: incident\ndata: {"id":"i1"}\n\n'));
      }
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

    renderDashboard("/dashboard/incidents");
    await waitFor(() => expect(screen.getByText("Active Incidents")).toBeInTheDocument());

    await waitFor(() => {
      const statsCalls = fetchMock.mock.calls.filter((c) => (c[0] as string).includes("/dashboard/stats")).length;
      const incidentCalls = fetchMock.mock.calls.filter((c) => (c[0] as string).startsWith("/api/v1/incidents")).length;
      expect(statsCalls).toBeGreaterThanOrEqual(2);
      expect(incidentCalls).toBeGreaterThanOrEqual(2);
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

  it("changing the time range re-fetches the follow-up view with a since query param", async () => {
    sessionWith(["followup"]);
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ incidents: [], alerts: [] }));
    vi.stubGlobal("fetch", fetchMock);

    renderDashboard("/dashboard/followup");
    expect(await screen.findByText("Nothing pending follow-up.")).toBeInTheDocument();

    await userEvent.selectOptions(screen.getByLabelText("Time range"), "24h");

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c: unknown[]) => c[0] as string);
      expect(calls.some((u) => u.startsWith("/api/v1/dashboard/followup?") && u.includes("since="))).toBe(true);
    });
  });
});
