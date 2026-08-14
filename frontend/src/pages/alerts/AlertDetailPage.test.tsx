import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { AlertDetailPage } from "./AlertDetailPage";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function alertFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "a1", title: "Suspicious login", source: "wazuh", severity: "high", originalSeverity: "high", status: "open",
    tags: [], payload: { raw: true }, receivedAt: "2026-01-01T00:00:00Z", duplicateCount: 0, ...overrides,
  };
}

// Order matters: "/api/v1/alerts/a1/alerts" contains "/api/v1/alerts/a1" as a
// substring, so the more specific routes must be checked first.
function routeFetch(alert: Record<string, unknown>) {
  return vi.fn().mockImplementation((url: string) => {
    if (url.includes("/api/v1/alerts/a1/analyze/messages")) return Promise.resolve(jsonResponse({ messages: [] }));
    if (url.includes("/api/v1/alerts/a1/alerts")) return Promise.resolve(jsonResponse([]));
    if (url.includes("/api/v1/alerts/a1/comments")) return Promise.resolve(jsonResponse([]));
    if (url.includes("/api/v1/alerts/a1")) return Promise.resolve(jsonResponse(alert));
    if (url.includes("/api/v1/alerts?")) return Promise.resolve(jsonResponse([]));
    if (url.includes("/api/v1/playbooks/match")) return Promise.resolve(jsonResponse(null));
    if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
    if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([]));
    return Promise.resolve(jsonResponse({}));
  });
}

function renderDetail() {
  return render(
    <MemoryRouter initialEntries={["/alerts/a1"]}>
      <AuthProvider>
        <Routes>
          <Route path="/alerts/:id" element={<AlertDetailPage />} />
          <Route path="/incidents/:id" element={<div>Incident Page</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("AlertDetailPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders the alert's title, severity, and status", async () => {
    vi.stubGlobal("fetch", routeFetch(alertFixture()));
    renderDetail();

    expect(await screen.findByRole("heading", { name: "Suspicious login" })).toBeInTheDocument();
    // "High"/"Open" also appear as <option> text in the Severity Override
    // panel's dropdowns, so assert on the badge specifically rather than
    // getByText (which would throw on multiple matches).
    expect(screen.getByText("High", { selector: ".badge" })).toBeInTheDocument();
    expect(screen.getByText("Open", { selector: ".badge" })).toBeInTheDocument();
  });

  it("shows the backend's error message when the alert doesn't exist", async () => {
    // Matches the real handler's 404 shape (writeError writes {"error": "..."}).
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "alert not found" }, 404)));
    renderDetail();

    expect(await screen.findByText("alert not found")).toBeInTheDocument();
  });

  it("Start Investigating only appears while the alert is open", async () => {
    vi.stubGlobal("fetch", routeFetch(alertFixture({ status: "investigating" })));
    renderDetail();

    await screen.findByRole("heading", { name: "Suspicious login" });
    expect(screen.queryByRole("button", { name: "Start Investigating" })).not.toBeInTheDocument();
  });

  it("clicking Start Investigating assigns the alert to the logged-in analyst and marks it investigating", async () => {
    localStorage.setItem(
      "argusops.session",
      JSON.stringify({
        token: "tok",
        refreshToken: "rt",
        user: { id: "analyst-1", email: "a@b.com", name: "Analyst One", role: "Analyst", mustChangePassword: false, isAdmin: false, resourceAccess: [] },
      }),
    );
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/api/v1/alerts/a1/assignee")) return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/api/v1/alerts/a1/status")) return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/api/v1/alerts/a1/alerts")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1/comments")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1")) return Promise.resolve(jsonResponse(alertFixture()));
      if (url.includes("/api/v1/alerts?")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/playbooks/match")) return Promise.resolve(jsonResponse(null));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([]));
      void init;
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Start Investigating" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/assignee",
        expect.objectContaining({ method: "PUT", body: JSON.stringify({ analystId: "analyst-1" }) }),
      ),
    );
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/alerts/a1/status",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ status: "investigating" }) }),
    );
  });

  it("shows Escalate to Incident unless the alert is already escalated", async () => {
    vi.stubGlobal("fetch", routeFetch(alertFixture({ status: "escalated" })));
    renderDetail();

    await screen.findByRole("heading", { name: "Suspicious login" });
    expect(screen.queryByRole("button", { name: "Escalate to Incident" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Analyze with AI" })).toBeInTheDocument();
  });

  it("a closed alert shows no escalate button", async () => {
    vi.stubGlobal("fetch", routeFetch(alertFixture({ status: "closed", classification: "true_positive" })));
    renderDetail();

    await screen.findByRole("heading", { name: "Suspicious login" });
    expect(screen.queryByRole("button", { name: "Escalate to Incident" })).not.toBeInTheDocument();
  });

  it("an already closed and classified alert shows no Close & Classify button, only the read-only summary", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch(alertFixture({ status: "closed", classification: "true_positive", closeComment: "Confirmed benign" })),
    );
    renderDetail();

    await screen.findByRole("heading", { name: "Suspicious login" });
    expect(screen.queryByRole("button", { name: "Close & Classify Alert" })).not.toBeInTheDocument();
    expect(screen.getByText("Confirmed benign")).toBeInTheDocument();
  });

  it("Close & Classify Alert opens a modal that can be dismissed without submitting", async () => {
    const fetchMock = routeFetch(alertFixture());
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Close & Classify Alert" }));
    expect(screen.getByPlaceholderText("Add a closing comment...")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByPlaceholderText("Add a closing comment...")).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/alerts/a1/close", expect.anything());
  });

  it("clicking Escalate to Incident posts to the escalate endpoint and navigates", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/alerts/a1/alerts")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1/comments")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1/escalate")) return Promise.resolve(jsonResponse({ incidentId: "inc-1" }));
      if (url.includes("/api/v1/alerts/a1")) return Promise.resolve(jsonResponse(alertFixture()));
      if (url.includes("/api/v1/alerts?")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/playbooks/match")) return Promise.resolve(jsonResponse(null));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Escalate to Incident" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/escalate",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    expect(await screen.findByText("Incident Page")).toBeInTheDocument();
  });

  it("opening the close form and submitting posts classification+comment", async () => {
    const fetchMock = routeFetch(alertFixture());
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Close & Classify Alert" }));
    await userEvent.type(screen.getByPlaceholderText("Add a closing comment..."), "Confirmed benign");
    await userEvent.click(screen.getByRole("button", { name: "Confirm & Close Alert" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/close",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ classification: "true_positive", comment: "Confirmed benign", attachmentUrl: null }) }),
      ),
    );
  });

  it("attaching an image in the close form uploads it and includes the url when closing", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url === "/api/v1/uploads/images") return Promise.resolve(jsonResponse({ url: "/api/v1/uploads/images/Alert/2026/01/01/a1_x/f.png" }));
      if (url.includes("/api/v1/alerts/a1/alerts")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1/comments")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1")) return Promise.resolve(jsonResponse(alertFixture()));
      if (url.includes("/api/v1/alerts?")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/playbooks/match")) return Promise.resolve(jsonResponse(null));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Close & Classify Alert" }));
    const file = new File(["binary"], "screenshot.png", { type: "image/png" });
    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(fileInput, file);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("/api/v1/uploads/images"), expect.anything()));

    await userEvent.click(screen.getByRole("button", { name: "Confirm & Close Alert" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/close",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ classification: "true_positive", comment: "", attachmentUrl: "/api/v1/uploads/images/Alert/2026/01/01/a1_x/f.png" }),
        }),
      ),
    );
  });

  it("renders the linked incident button when incidentId is set", async () => {
    vi.stubGlobal("fetch", routeFetch(alertFixture({ incidentId: "inc-1" })));
    renderDetail();

    expect(await screen.findByRole("button", { name: "View linked incident" })).toBeInTheDocument();
  });

  it("clicking Analyze with AI opens the analysis chat instead of triggering a one-shot analysis", async () => {
    const fetchMock = routeFetch(alertFixture());
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.click(await screen.findByRole("button", { name: "Analyze with AI" }));

    expect(await screen.findByRole("heading", { name: "Analyze with AI" })).toBeInTheDocument();
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/alerts/a1/analyze/messages", expect.anything()),
    );
    // The old one-shot endpoint is never hit anymore -- opening the chat
    // only GETs the transcript, it doesn't itself start an analysis.
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/alerts/a1/analyze", expect.anything());
  });

  it("shows Analyzing... on the button while a background analysis is in progress", async () => {
    vi.stubGlobal("fetch", routeFetch(alertFixture({ latestAnalysisStatus: "running" })));
    renderDetail();

    expect(await screen.findByRole("button", { name: "Analyzing..." })).toBeInTheDocument();
  });

  it("shows the Metadata panel with a clickable link for URL-shaped values", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch(alertFixture({ metadata: { slackChannel: "#incident-response", playbookUrl: "https://runbooks.example.com/brute-force" } })),
    );
    renderDetail();

    expect(await screen.findByText("Custom Metadata")).toBeInTheDocument();
    expect(screen.getByText("slackChannel")).toBeInTheDocument();
    expect(screen.getByText("#incident-response")).toBeInTheDocument();

    const link = screen.getByRole("link", { name: "https://runbooks.example.com/brute-force" });
    expect(link).toHaveAttribute("href", "https://runbooks.example.com/brute-force");
    expect(link).toHaveAttribute("target", "_blank");
  });

  it("hides the Metadata panel entirely when there's no metadata", async () => {
    vi.stubGlobal("fetch", routeFetch(alertFixture({ metadata: {} })));
    renderDetail();

    await screen.findByRole("heading", { name: "Suspicious login" });
    expect(screen.queryByText("Custom Metadata")).not.toBeInTheDocument();
  });

  it("changing severity in the override panel shows Save, and saving PUTs the new severity", async () => {
    const fetchMock = routeFetch(alertFixture());
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await screen.findByRole("heading", { name: "Suspicious login" });
    const selects = screen.getAllByRole("combobox");
    // The Manual Severity Override panel's severity <select> has no
    // associated <label> (only a helper-text line above it, per the
    // design), so it's targeted positionally -- it's the only unlabeled,
    // unnamed combobox on the page (the Assignee panel's select has an
    // aria-label, so it's excluded by the accessible-name check).
    const severitySelect = selects.find((s) => !s.hasAttribute("id") && !s.hasAttribute("aria-label"))!;
    await userEvent.selectOptions(severitySelect, "critical");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/severity",
        expect.objectContaining({ method: "PUT", body: JSON.stringify({ severity: "critical" }) }),
      ),
    );
  });

  it("severity override is read-only once the alert is closed", async () => {
    vi.stubGlobal("fetch", routeFetch(alertFixture({ status: "closed", classification: "true_positive" })));
    renderDetail();

    await screen.findByRole("heading", { name: "Suspicious login" });
    const selects = screen.getAllByRole("combobox");
    const severitySelect = selects.find((s) => !s.hasAttribute("id") && !s.hasAttribute("aria-label"))!;
    expect(severitySelect).toBeDisabled();
  });

  it("linking an alert from the search results PUTs the link endpoint", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/alerts/a1/alerts/a2")) return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/api/v1/alerts/a1/alerts")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1/comments")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1")) return Promise.resolve(jsonResponse(alertFixture()));
      if (url.includes("/api/v1/alerts?")) return Promise.resolve(jsonResponse([{ id: "a2", title: "Outbound C2 traffic", source: "suricata", severity: "critical", status: "open" }]));
      if (url.includes("/api/v1/playbooks/match")) return Promise.resolve(jsonResponse(null));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.type(await screen.findByPlaceholderText("Search alerts by ID or title to link..."), "Outbound");
    await userEvent.click(await screen.findByText(/Outbound C2 traffic/));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/alerts/a1/alerts/a2", expect.objectContaining({ method: "PUT" })),
    );
  });

  it("renders existing Team Notes comments", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/alerts/a1/alerts")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1/comments")) {
        return Promise.resolve(
          jsonResponse([
            { id: "c1", alertId: "a1", tenantId: "t1", authorId: "u1", authorName: "Marina Alves", body: "Confirmed source IP is a known scanner.", createdAt: "2026-01-01T00:00:00Z" },
          ]),
        );
      }
      if (url.includes("/api/v1/alerts/a1")) return Promise.resolve(jsonResponse(alertFixture()));
      if (url.includes("/api/v1/alerts?")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/playbooks/match")) return Promise.resolve(jsonResponse(null));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    expect(await screen.findByText("Marina Alves")).toBeInTheDocument();
    expect(screen.getByText("Confirmed source IP is a known scanner.")).toBeInTheDocument();
  });

  it("selecting an analyst in the Assignee panel PUTs the assignee endpoint", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/users/directory")) {
        return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }]));
      }
      if (url.includes("/api/v1/alerts/a1/alerts")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1/comments")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/alerts/a1")) return Promise.resolve(jsonResponse(alertFixture()));
      if (url.includes("/api/v1/alerts?")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/api/v1/playbooks/match")) return Promise.resolve(jsonResponse(null));
      if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.selectOptions(await screen.findByLabelText("Assigned Analyst"), "u1");

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/assignee",
        expect.objectContaining({ method: "PUT", body: JSON.stringify({ analystId: "u1" }) }),
      ),
    );
  });

  it("posting a Team Note submits to the alert comments endpoint", async () => {
    const fetchMock = routeFetch(alertFixture());
    vi.stubGlobal("fetch", fetchMock);
    renderDetail();

    await userEvent.type(await screen.findByPlaceholderText("Add a note for the team..."), "Investigating further");
    await userEvent.click(screen.getByRole("button", { name: "Post" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/comments",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ body: "Investigating further", attachmentUrl: null }) }),
      ),
    );
  });
});
