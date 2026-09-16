import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { IncidentsListPage } from "./IncidentsListPage";
import { AuthProvider } from "../../auth/AuthContext";
import { seedSession, withSession } from "../../test/session";

function wrapper({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter>
      <AuthProvider>{children}</AuthProvider>
    </MemoryRouter>
  );
}

function jsonResponse(body: unknown, status = 200, total?: number) {
  const headers: Record<string, string> = { "content-type": "application/json" };
  if (total !== undefined) headers["X-Total-Count"] = String(total);
  return new Response(JSON.stringify(body), { status, headers });
}

function incidentFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "i1", title: "Ransomware suspected", severity: "critical", priority: "p1",
    phase: "new", tags: [], assignees: [], slaBreached: false, openedAt: "2026-01-01T00:00:00Z", ...overrides,
  };
}

describe("IncidentsListPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders fetched incidents", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([incidentFixture()]))));
    render(<IncidentsListPage />, { wrapper });

    expect(await screen.findByText("Ransomware suspected")).toBeInTheDocument();
  });

  it("flags SLA-breached incidents distinctly", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([incidentFixture({ slaBreached: true })]))));
    render(<IncidentsListPage />, { wrapper });

    expect(await screen.findByText("SLA breached")).toBeInTheDocument();
  });

  it("shows the empty state with no results", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([]))));
    render(<IncidentsListPage />, { wrapper });

    expect(await screen.findByText("No incidents found for the current filters.")).toBeInTheDocument();
  });

  it("opens the create form, and a missing title blocks submission via the required attribute", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([]))));
    render(<IncidentsListPage />, { wrapper });
    await waitFor(() => expect(screen.getByText("No incidents found for the current filters.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Incident" }));
    expect(screen.getByLabelText("Title")).toBeRequired();
  });

  it("submitting the create form posts the incident payload", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ ...incidentFixture(), id: "new-id" }, 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });
    await waitFor(() => expect(screen.getByText("No incidents found for the current filters.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Incident" }));
    await userEvent.type(screen.getByLabelText("Title"), "Phishing wave");
    await userEvent.click(screen.getByRole("button", { name: "Create Incident" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });

  it("shows a validation error message returned by the API", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ error: "title is required" }, 400));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });
    await waitFor(() => expect(screen.getByText("No incidents found for the current filters.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Incident" }));
    const titleInput = screen.getByLabelText("Title") as HTMLInputElement;
    titleInput.removeAttribute("required");
    await userEvent.click(screen.getByRole("button", { name: "Create Incident" }));

    expect(await screen.findByText("title is required")).toBeInTheDocument();
  });

  it("populates the assignee picker from the user directory and submits the chosen assigneeIds", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/users/directory")) {
        return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }, { id: "u2", name: "Diego Costa" }]));
      }
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ ...incidentFixture(), id: "new-id" }, 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });
    await waitFor(() => expect(screen.getByText("No incidents found for the current filters.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Incident" }));
    expect(await screen.findByText("Marina Alves")).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText("Title"), "Phishing wave");
    await userEvent.selectOptions(screen.getByDisplayValue("+ Add assignee..."), "u2");
    await userEvent.click(screen.getByRole("button", { name: "Create Incident" }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find((c: unknown[]) => (c[1] as RequestInit)?.method === "POST");
      expect(postCall).toBeDefined();
      const body = JSON.parse((postCall![1] as RequestInit).body as string);
      expect(body.assigneeIds).toEqual(["u2"]);
    });
  });

  it("changing the severity, priority, and phase filters re-fetches with the new query params", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[0], "critical");
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0] as string);
      expect(calls.some((u) => u.includes("severity=critical"))).toBe(true);
    });

    await userEvent.selectOptions(selects[1], "p1");
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0] as string);
      expect(calls.some((u) => u.includes("priority=p1"))).toBe(true);
    });

    await userEvent.selectOptions(selects[2], "containment");
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0] as string);
      expect(calls.some((u) => u.includes("phase=containment"))).toBe(true);
    });
  });

  it("the SLA filter re-fetches with the sla query param", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([incidentFixture()]));
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[3], "breached");
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0] as string);
      expect(calls.some((u) => u.includes("sla=breached"))).toBe(true);
    });

    await userEvent.selectOptions(selects[3], "ok");
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0] as string);
      expect(calls.some((u) => u.includes("sla=ok"))).toBe(true);
    });
  });

  it("typing in the search box re-fetches with a q query param, after debouncing", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const searchInput = screen.getByPlaceholderText("Search title or description...");
    await userEvent.type(searchInput, "ransomware");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("q=ransomware");
    });
  });

  it("choosing a time-range preset re-fetches with a since query param", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[4], "30d");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("since=");
    });
  });

  it("a custom time range sends both since and until", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const selects = screen.getAllByRole("combobox");
    await userEvent.selectOptions(selects[4], "custom");

    await userEvent.type(screen.getByLabelText("From"), "2026-01-01T00:00");
    await userEvent.type(screen.getByLabelText("To"), "2026-01-31T00:00");

    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const lastCall = calls[calls.length - 1]?.[0] as string;
      expect(lastCall).toContain("since=");
      expect(lastCall).toContain("until=");
    });
  });

  it("reloads the list when a live incident event comes in over the event stream", async () => {
    seedSession({ id: "1", email: "a@b.com", name: "A", role: "admin", mustChangePassword: false, resourceAccess: [] });

    const encoder = new TextEncoder();
    const streamResponse = new Response(
      new ReadableStream({
        start(controller) {
          // Deferred a macrotask so the frame lands after the initial
          // /api/v1/incidents GET has already resolved -- react-query
          // dedupes a reload() that lands while the initial fetch is still
          // in flight (no second request needed, it just rides the existing
          // one), same as a real SSE frame arriving after the page loaded.
          setTimeout(() => {
            controller.enqueue(encoder.encode('event: alert\ndata: {"id":"a1"}\n\nevent: incident\ndata: {"id":"i1"}\n\n'));
            controller.close();
          }, 0);
        },
      }),
      { status: 200 },
    );

    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.startsWith("/api/v1/events/stream")) return Promise.resolve(streamResponse);
      return Promise.resolve(jsonResponse([incidentFixture()]));
    });
    vi.stubGlobal("fetch", withSession(fetchMock));

    render(<IncidentsListPage />, { wrapper });
    await screen.findByText("Ransomware suspected");

    await waitFor(() => {
      const incidentCalls = fetchMock.mock.calls.filter((c) => (c[0] as string).startsWith("/api/v1/incidents")).length;
      expect(incidentCalls).toBeGreaterThanOrEqual(2);
    });
  });

  it("the × button on the create form dismisses it without submitting", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });
    await waitFor(() => expect(screen.getByText("No incidents found for the current filters.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Incident" }));
    expect(screen.getByLabelText("Title")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByLabelText("Title")).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/incidents", expect.objectContaining({ method: "POST" }));
  });

  it("renders tag chips and joined assignee names for an incident that has them", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(
        jsonResponse([
          incidentFixture({
            tags: ["ransomware", "priority"],
            assignees: [{ id: "u1", name: "Marina Alves" }, { id: "u2", name: "Diego Costa" }],
          }),
        ]),
      )));
    render(<IncidentsListPage />, { wrapper });

    await screen.findByText("Ransomware suspected");
    expect(screen.getByText("ransomware")).toBeInTheDocument();
    expect(screen.getByText("priority")).toBeInTheDocument();
    expect(screen.getByText("Marina Alves, Diego Costa")).toBeInTheDocument();
  });

  it("shows 'Closed' for a closed incident regardless of SLA state", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([incidentFixture({ slaBreached: true, closedAt: "2026-01-02T00:00:00Z" })]))));
    render(<IncidentsListPage />, { wrapper });

    expect(await screen.findByText("Closed")).toBeInTheDocument();
  });

  it("shows time-remaining copy for an open incident with an unbreached SLA due date", async () => {
    const future = new Date(Date.now() + 3600_000).toISOString();
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([incidentFixture({ slaBreached: false, slaDueAt: future })]))));
    render(<IncidentsListPage />, { wrapper });

    await screen.findByText("Ransomware suspected");
    expect(screen.getByText(/remaining/)).toBeInTheDocument();
  });

  it("includes the relative time-ago for a breached SLA that has a due date", async () => {
    const past = new Date(Date.now() - 3600_000).toISOString();
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([incidentFixture({ slaBreached: true, slaDueAt: past })]))));
    render(<IncidentsListPage />, { wrapper });

    expect(await screen.findByText(/SLA breached .+ ago/)).toBeInTheDocument();
  });

  it("changing severity, priority, and description in the create form includes them in the payload", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ ...incidentFixture(), id: "new-id" }, 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", withSession(fetchMock));
    render(<IncidentsListPage />, { wrapper });
    await waitFor(() => expect(screen.getByText("No incidents found for the current filters.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Incident" }));
    await userEvent.type(screen.getByLabelText("Title"), "Phishing wave");
    await userEvent.selectOptions(screen.getByLabelText("Severity"), "critical");
    await userEvent.selectOptions(screen.getByLabelText("Priority"), "p1");
    await userEvent.type(screen.getByLabelText("Description"), "Widespread phishing campaign");
    await userEvent.click(screen.getByRole("button", { name: "Create Incident" }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find((c: unknown[]) => (c[1] as RequestInit)?.method === "POST");
      expect(postCall).toBeDefined();
      const body = JSON.parse((postCall![1] as RequestInit).body as string);
      expect(body.severity).toBe("critical");
      expect(body.priority).toBe("p1");
      expect(body.description).toBe("Widespread phishing campaign");
    });
  });

  it("shows pagination totals and fetches the next page (offset) on click", async () => {
    const page1 = Array.from({ length: 20 }, (_, i) => incidentFixture({ id: `i${i}`, title: `Incident ${i}` }));
    const page2 = [incidentFixture({ id: "extra", title: "Extra incident" })];
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(page1, 200, 21))
      .mockResolvedValueOnce(jsonResponse(page2, 200, 21));
    vi.stubGlobal("fetch", withSession(fetchMock));

    render(<IncidentsListPage />, { wrapper });
    await screen.findByText("Incident 0");
    expect(screen.getByText("Showing 1-20 of 21")).toBeInTheDocument();

    const nextBtn = screen.getByRole("button", { name: "Next" });
    await userEvent.click(nextBtn);
    expect(await screen.findByText("Extra incident")).toBeInTheDocument();

    const calls = fetchMock.mock.calls;
    const lastCall = calls[calls.length - 1]?.[0] as string;
    expect(lastCall).toContain("limit=20");
    expect(lastCall).toContain("offset=20");
  });

  describe("bulk phase-change", () => {
    it("selecting a row shows the bulk toolbar with a count, and it disappears on clear", async () => {
      vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([incidentFixture()]))));
      render(<IncidentsListPage />, { wrapper });
      await screen.findByText("Ransomware suspected");

      expect(screen.queryByText("1 selected")).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole("checkbox", { name: /Select incident/ }));
      expect(await screen.findByText("1 selected")).toBeInTheDocument();

      await userEvent.click(screen.getByRole("button", { name: "Clear" }));
      expect(screen.queryByText("1 selected")).not.toBeInTheDocument();
    });

    it("the header checkbox selects and deselects every row on the page", async () => {
      vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([incidentFixture({ id: "i1" }), incidentFixture({ id: "i2", title: "Second incident" })]))));
      render(<IncidentsListPage />, { wrapper });
      await screen.findByText("Ransomware suspected");

      await userEvent.click(screen.getByRole("checkbox", { name: "Select all incidents on this page" }));
      expect(await screen.findByText("2 selected")).toBeInTheDocument();

      await userEvent.click(screen.getByRole("checkbox", { name: "Select all incidents on this page" }));
      expect(screen.queryByText(/selected/)).not.toBeInTheDocument();
    });

    it("the bulk phase dropdown never offers post_incident -- bulk-close is descoped", async () => {
      vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse([incidentFixture()]))));
      render(<IncidentsListPage />, { wrapper });
      await screen.findByText("Ransomware suspected");

      await userEvent.click(screen.getByRole("checkbox", { name: /Select incident/ }));
      const bulkSelect = screen.getByRole("combobox", { name: "Change phase to..." });
      const optionLabels = Array.from(bulkSelect.querySelectorAll("option")).map((o) => o.textContent);
      expect(optionLabels).not.toContain("Post-Incident");
    });

    it("applying a bulk phase change posts the selected ids and shows a success summary", async () => {
      const fetchMock = vi.fn().mockImplementation((url: string) => {
        if (url === "/api/v1/incidents/bulk/phase") {
          return Promise.resolve(jsonResponse({ results: [{ id: "i1", success: true }] }));
        }
        return Promise.resolve(jsonResponse([incidentFixture()]));
      });
      vi.stubGlobal("fetch", withSession(fetchMock));
      render(<IncidentsListPage />, { wrapper });
      await screen.findByText("Ransomware suspected");

      await userEvent.click(screen.getByRole("checkbox", { name: /Select incident/ }));
      await userEvent.click(screen.getByRole("button", { name: "Apply" }));

      await waitFor(() => {
        const call = fetchMock.mock.calls.find((c) => c[0] === "/api/v1/incidents/bulk/phase");
        expect(call).toBeTruthy();
        const body = JSON.parse((call![1] as RequestInit).body as string);
        expect(body).toEqual({ ids: ["i1"], phase: "new" });
      });
      expect(await screen.findByText("1 incident updated.")).toBeInTheDocument();
      expect(screen.queryByText(/selected/)).not.toBeInTheDocument();
    });

    it("a partial failure shows the success/failed breakdown", async () => {
      const fetchMock = vi.fn().mockImplementation((url: string) => {
        if (url === "/api/v1/incidents/bulk/phase") {
          return Promise.resolve(
            jsonResponse({
              results: [
                { id: "i1", success: true },
                { id: "i2", success: false, error: "incident i2 not found" },
              ],
            }),
          );
        }
        return Promise.resolve(jsonResponse([incidentFixture({ id: "i1" }), incidentFixture({ id: "i2", title: "Second incident" })]));
      });
      vi.stubGlobal("fetch", withSession(fetchMock));
      render(<IncidentsListPage />, { wrapper });
      await screen.findByText("Ransomware suspected");

      await userEvent.click(screen.getByRole("checkbox", { name: "Select all incidents on this page" }));
      await userEvent.click(screen.getByRole("button", { name: "Apply" }));

      expect(await screen.findByText("1 updated, 1 failed.")).toBeInTheDocument();
    });
  });
});
