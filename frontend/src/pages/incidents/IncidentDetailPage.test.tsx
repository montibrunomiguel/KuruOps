import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { IncidentDetailPage } from "./IncidentDetailPage";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function incidentFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "i1", title: "Ransomware suspected", description: "Encrypted files found",
    severity: "critical", priority: "p1", phase: "new", tags: [], assignees: [], roles: [],
    slaBreached: false, openedAt: "2026-01-01T00:00:00Z", ...overrides,
  };
}

function routeFetch(
  incident: Record<string, unknown>,
  opts: { events?: unknown[]; statusHistory?: unknown[]; linkedAlerts?: unknown[]; comments?: unknown[] } = {},
) {
  return vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (url.includes("/status-history")) {
      if (init?.method === "POST") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(opts.statusHistory ?? []));
    }
    if (url.includes("/postmortem")) {
      return Promise.resolve(
        new Response("# Postmortem: Ransomware suspected", {
          status: 200,
          headers: {
            "content-type": "text/markdown; charset=utf-8",
            "content-disposition": 'attachment; filename="postmortem-i1.md"',
          },
        }),
      );
    }
    if (url.includes("/timeline")) return Promise.resolve(jsonResponse(opts.events ?? []));
    if (url.includes("/comments")) {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ id: "c1" }, 201));
      return Promise.resolve(jsonResponse(opts.comments ?? []));
    }
    if (url.match(/\/incidents\/i1\/alerts\/[^/]+$/)) return Promise.resolve(new Response(null, { status: 204 }));
    if (url.includes("/alerts")) return Promise.resolve(jsonResponse(opts.linkedAlerts ?? []));
    if (url.includes("/api/v1/incidents/i1")) return Promise.resolve(jsonResponse(incident));
    if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
    if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([]));
    return Promise.resolve(jsonResponse({}));
  });
}

function renderDetail() {
  return render(
    <MemoryRouter initialEntries={["/incidents/i1"]}>
      <AuthProvider>
        <Routes>
          <Route path="/incidents/:id" element={<IncidentDetailPage />} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("IncidentDetailPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders title, severity, and priority", async () => {
    vi.stubGlobal("fetch", routeFetch(incidentFixture()));
    renderDetail();

    expect(await screen.findByRole("heading", { name: "Ransomware suspected" })).toBeInTheDocument();
    expect(screen.getAllByText("Critical").length).toBeGreaterThan(0);
    expect(screen.getAllByText("P1").length).toBeGreaterThan(0);
  });

  it("Close is disabled until phase is post_incident", async () => {
    vi.stubGlobal("fetch", routeFetch(incidentFixture({ phase: "new" })));
    renderDetail();

    expect(await screen.findByRole("button", { name: "Close Incident" })).toBeDisabled();
  });

  it("Close is enabled once phase is post_incident", async () => {
    vi.stubGlobal("fetch", routeFetch(incidentFixture({ phase: "post_incident" })));
    renderDetail();

    expect(await screen.findByRole("button", { name: "Close Incident" })).toBeEnabled();
  });

  it("Generate Postmortem only appears once phase is post_incident", async () => {
    vi.stubGlobal("fetch", routeFetch(incidentFixture({ phase: "new" })));
    renderDetail();

    await screen.findByRole("heading", { name: "Ransomware suspected" });
    expect(screen.queryByRole("button", { name: "Generate Postmortem" })).not.toBeInTheDocument();
  });

  it("Generate Postmortem stays visible after the incident is closed", async () => {
    vi.stubGlobal("fetch", routeFetch(incidentFixture({ phase: "post_incident", closedAt: "2026-01-02T00:00:00Z" })));
    renderDetail();

    expect(await screen.findByRole("button", { name: "Generate Postmortem" })).toBeInTheDocument();
  });

  it("clicking Generate Postmortem fetches the postmortem endpoint and triggers a download", async () => {
    // jsdom doesn't implement the Blob-URL APIs the download flow uses.
    vi.stubGlobal("URL", { ...URL, createObjectURL: vi.fn(() => "blob:mock"), revokeObjectURL: vi.fn() });
    const fetchMock = routeFetch(incidentFixture({ phase: "post_incident" }));
    vi.stubGlobal("fetch", fetchMock);
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    renderDetail();
    await userEvent.click(await screen.findByRole("button", { name: "Generate Postmortem" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/incidents/i1/postmortem", expect.objectContaining({ headers: expect.any(Object) })),
    );
    expect(clickSpy).toHaveBeenCalled();

    clickSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("a closed incident shows no close button", async () => {
    vi.stubGlobal("fetch", routeFetch(incidentFixture({ phase: "post_incident", closedAt: "2026-01-02T00:00:00Z" })));
    renderDetail();

    await screen.findByRole("heading", { name: "Ransomware suspected" });
    expect(screen.queryByRole("button", { name: "Close Incident" })).not.toBeInTheDocument();
    expect(screen.getByText("Closed")).toBeInTheDocument();
  });

  it("clicking a phase step posts a phase change", async () => {
    const fetchMock = routeFetch(incidentFixture());
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Containment" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents/i1/phase",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });

  it("marks a phase_skipped timeline event with a warning badge", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch(incidentFixture(), {
        events: [
          { id: 1, incidentId: "i1", eventType: "phase_skipped", actorType: "system", data: { from: "new", to: "containment" }, createdAt: "2026-01-01T00:00:00Z" },
        ],
      }),
    );
    renderDetail();

    expect(await screen.findByText("attention")).toBeInTheDocument();
  });

  it("editing the description saves and exits edit mode", async () => {
    const fetchMock = routeFetch(incidentFixture());
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    const textarea = screen.getByDisplayValue("Encrypted files found");
    await userEvent.clear(textarea);
    await userEvent.type(textarea, "Updated description");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents/i1/description",
        expect.objectContaining({ method: "PUT", body: JSON.stringify({ description: "Updated description" }) }),
      ),
    );
  });

  it("clicking a NIST matrix cell immediately applies that severity/priority", async () => {
    const fetchMock = routeFetch(incidentFixture({ severity: "high", priority: "p2" }));
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await screen.findByRole("heading", { name: "Ransomware suspected" });
    await userEvent.click(screen.getByRole("button", { name: "Critical / P1" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents/i1/severity-priority",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ severity: "critical", priority: "p1" }) }),
      ),
    );
  });

  it("the standalone Severity & Priority panel no longer exists", async () => {
    vi.stubGlobal("fetch", routeFetch(incidentFixture()));
    renderDetail();

    await screen.findByRole("heading", { name: "Ransomware suspected" });
    expect(screen.queryByLabelText("Priority")).not.toBeInTheDocument();
  });

  it("linking an alert by ID PUTs to the link endpoint and renders the linked row", async () => {
    const fetchMock = routeFetch(incidentFixture(), {
      linkedAlerts: [{ id: "a1", title: "Suspicious login", source: "wazuh", status: "open", severity: "high" }],
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.type(await screen.findByPlaceholderText("Alert ID to link"), "a1");
    await userEvent.click(screen.getByRole("button", { name: "Link" }));

    expect(await screen.findByText("Suspicious login")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/incidents/i1/alerts/a1", expect.objectContaining({ method: "PUT" }));
  });

  it("unlinking a linked alert calls the delete endpoint", async () => {
    const fetchMock = routeFetch(incidentFixture(), {
      linkedAlerts: [{ id: "a1", title: "Suspicious login", source: "wazuh", status: "open", severity: "high" }],
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Unlink" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/incidents/i1/alerts/a1", expect.objectContaining({ method: "DELETE" })),
    );
  });

  // StatusHistoryPanel shows an always-visible datetime-local input per
  // phase (no toggle button) -- the reason field only appears once the
  // input's value actually diverges from the entry's current effective
  // time (see StatusHistoryRow's `dirty` check).
  it("status history shows entered phases, and correcting one requires a reason", async () => {
    const fetchMock = routeFetch(incidentFixture(), {
      statusHistory: [
        { id: "h1", incidentId: "i1", tenantId: "t1", phase: "new", enteredAt: "2026-01-01T00:00:00Z", createdAt: "2026-01-01T00:00:00Z" },
      ],
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    const dtInput = await screen.findByLabelText("Correct time — New");
    fireEvent.change(dtInput, { target: { value: "2026-01-05T10:00" } });

    // The reason textarea is also HTML-required, so a genuinely empty value
    // never reaches handleSubmit at all (native validation blocks it first)
    // -- a whitespace-only value passes that native check but still fails
    // the app's own `!reason.trim()` guard, which is the branch under test.
    await userEvent.type(
      screen.getByPlaceholderText("e.g. timestamp recorded wrong due to a delay in the source alert's ingestion"),
      " ",
    );
    await userEvent.click(screen.getByRole("button", { name: "Save correction" }));

    expect(await screen.findByText("A reason is required to correct a timestamp.")).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith(expect.stringContaining("/correct"), expect.anything());
  });

  it("a filled-in correction posts the new timestamp and reason", async () => {
    const fetchMock = routeFetch(incidentFixture(), {
      statusHistory: [
        { id: "h1", incidentId: "i1", tenantId: "t1", phase: "new", enteredAt: "2026-01-01T00:00:00Z", createdAt: "2026-01-01T00:00:00Z" },
      ],
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    const dtInput = await screen.findByLabelText("Correct time — New");
    fireEvent.change(dtInput, { target: { value: "2026-01-05T10:00" } });

    await userEvent.type(
      screen.getByPlaceholderText("e.g. timestamp recorded wrong due to a delay in the source alert's ingestion"),
      "backdated per SOC log",
    );
    await userEvent.click(screen.getByRole("button", { name: "Save correction" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents/i1/status-history/new/correct",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });

  it("submitting a comment posts it and clears the textarea", async () => {
    const fetchMock = routeFetch(incidentFixture());
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await screen.findByRole("heading", { name: "Ransomware suspected" });
    const input = screen.getByPlaceholderText("Add a note for the team...");
    await userEvent.type(input, "Investigating now");
    await userEvent.click(screen.getByRole("button", { name: "Post" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents/i1/comments",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });

  it("clicking Analyze with AI posts to the analyze endpoint (202) and shows the eventual result", async () => {
    let incidentCalls = 0;
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/api/v1/incidents/i1/analyze")) return Promise.resolve(jsonResponse({ status: "running" }, 202));
      if (url.includes("/api/v1/incidents/i1") && !url.includes("/incidents/i1/")) {
        incidentCalls++;
        // 1st fetch (initial mount): no analysis yet. 2nd fetch (the
        // reload() analyze() triggers right after the 202): stands in for
        // the real flow's SSE-driven reload once the background LLM call
        // finishes.
        const fixture =
          incidentCalls === 1
            ? incidentFixture()
            : incidentFixture({ latestAnalysisStatus: "completed", latestAnalysis: "recommend immediate containment" });
        return Promise.resolve(jsonResponse(fixture));
      }
      return routeFetch(incidentFixture())(url, init);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Analyze with AI" }));

    expect(await screen.findByText("recommend immediate containment")).toBeInTheDocument();
  });

  it("shows a running-analysis message while a background analysis is in progress", async () => {
    vi.stubGlobal("fetch", routeFetch(incidentFixture({ latestAnalysisStatus: "running" })));
    renderDetail();

    expect(await screen.findByText(/Analysis in progress/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Analyzing..." })).toBeDisabled();
  });

  it("shows a failed-analysis message with a retry button", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch(incidentFixture({ latestAnalysisStatus: "failed", latestAnalysisError: "no LLM provider configured" })),
    );
    renderDetail();

    expect(await screen.findByText("AI Analysis Failed")).toBeInTheDocument();
    expect(screen.getByText("no LLM provider configured")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });

  it("highlights the incident's own severity/priority cell in the NIST matrix", async () => {
    vi.stubGlobal("fetch", routeFetch(incidentFixture({ severity: "high", priority: "p2" })));
    renderDetail();

    await screen.findByRole("heading", { name: "Ransomware suspected" });
    const matrix = screen.getByText("NIST Severity × Priority Matrix").closest(".panel")!;
    const activeCell = matrix.querySelector('.nist-matrix-cell[data-active="true"]');
    expect(activeCell).not.toBeNull();
    // 4 priority columns (P1-P4) x the "High" row means the active cell is
    // the 6th grid cell overall (1 row label + 4 cells for Critical, then
    // High's row label, then P1, P2 -- P2 is the active one).
    expect(matrix.querySelectorAll('.nist-matrix-cell[data-active="true"]')).toHaveLength(1);
  });

  it("renders the Team Roles section with existing role assignments", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/api/v1/users/directory")) {
        return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }, { id: "u2", name: "Diego Costa" }]));
      }
      return routeFetch(
        incidentFixture({
          roles: [
            { role: "commander", user: { id: "u1", name: "Marina Alves" } },
            { role: "incident_handler", user: { id: "u2", name: "Diego Costa" } },
          ],
        }),
      )(url, init);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await screen.findByText("Team Roles");
    expect(screen.getByText("Incident Commander")).toBeInTheDocument();
    expect(screen.getByDisplayValue("Marina Alves")).toBeInTheDocument();

    const handlerPanel = within(screen.getByText("Incident Handler(s)").closest("div") as HTMLElement);
    expect(handlerPanel.getByText("Diego Costa")).toBeInTheDocument();
  });

  it("assigning a Commander PUTs the role endpoint with a single userId", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/api/v1/users/directory")) {
        return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }]));
      }
      if (url.includes("/roles/commander")) return Promise.resolve(new Response(null, { status: 204 }));
      return routeFetch(incidentFixture())(url, init);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await screen.findByText("Team Roles");
    const commanderSelect = screen.getByLabelText("Incident Commander");
    await userEvent.selectOptions(commanderSelect, "Marina Alves");

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents/i1/roles/commander",
        expect.objectContaining({ method: "PUT", body: JSON.stringify({ userIds: ["u1"] }) }),
      ),
    );
  });

  it("adding a person to a multi-assignee role PUTs the role endpoint with the full list", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/api/v1/users/directory")) {
        return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }]));
      }
      if (url.includes("/roles/privacy_officer")) return Promise.resolve(new Response(null, { status: 204 }));
      return routeFetch(incidentFixture())(url, init);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await screen.findByText("Team Roles");
    const privacyPanel = within(screen.getByText("Privacy Officer").closest("div") as HTMLElement);
    await userEvent.selectOptions(privacyPanel.getByDisplayValue("+ Add assignee..."), "Marina Alves");

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents/i1/roles/privacy_officer",
        expect.objectContaining({ method: "PUT", body: JSON.stringify({ userIds: ["u1"] }) }),
      ),
    );
  });
});
