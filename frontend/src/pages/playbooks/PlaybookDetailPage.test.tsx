import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { PlaybookDetailPage } from "./PlaybookDetailPage";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function playbookFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "p1", title: "Phishing Response", category: "Phishing", description: "Standard triage",
    keywords: ["phishing"], steps: { detection_analysis: ["Check headers"] }, ...overrides,
  };
}

function renderDetail(id: string) {
  return render(
    <MemoryRouter initialEntries={[`/playbooks/${id}`]}>
      <AuthProvider>
        <Routes>
          <Route path="/playbooks/:id" element={<PlaybookDetailPage />} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("PlaybookDetailPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders an existing playbook read-only, with its steps", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbookFixture())));
    renderDetail("p1");

    expect(await screen.findByRole("heading", { name: "Phishing Response" })).toBeInTheDocument();
    expect(screen.getByText("Check headers")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
  });

  it("shows the empty create form immediately for /playbooks/new", async () => {
    vi.stubGlobal("fetch", vi.fn());
    renderDetail("new");

    expect(await screen.findByPlaceholderText("Playbook title")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
  });

  it("rejects saving a new playbook with an empty title, without calling the API", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("new");

    await screen.findByPlaceholderText("Playbook title");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Title is required")).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("creating a new playbook posts and navigates to it", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(playbookFixture({ id: "new-id" }), 201));
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("new");

    await userEvent.type(await screen.findByPlaceholderText("Playbook title"), "Phishing Response");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/playbooks",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });

  it("clicking Edit switches to edit mode with existing values prefilled", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbookFixture())));
    renderDetail("p1");

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    expect(screen.getByDisplayValue("Phishing Response")).toBeInTheDocument();
    expect(screen.getByDisplayValue("Standard triage")).toBeInTheDocument();
  });

  it("adding a step in edit mode inserts an empty input for it", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbookFixture({ steps: {} }))));
    renderDetail("p1");

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    const addButtons = screen.getAllByRole("button", { name: "+ Add step" });
    await userEvent.click(addButtons[0]);

    expect(screen.getByPlaceholderText("Step 1")).toBeInTheDocument();
  });

  it("deleting requires an inline confirm click, and Cancel backs out without deleting", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(playbookFixture()));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("p1");

    await userEvent.click(await screen.findByRole("button", { name: "Delete" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/playbooks/p1", expect.objectContaining({ method: "DELETE" }));
    expect(screen.getByText("Delete this playbook? This action cannot be undone.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText("Delete this playbook? This action cannot be undone.")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/playbooks/p1", expect.objectContaining({ method: "DELETE" })),
    );
  });

  it("shows 'not found' for an unknown playbook id", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "playbook not found" }, 404)));
    renderDetail("does-not-exist");

    expect(await screen.findByText("playbook not found")).toBeInTheDocument();
  });
});
