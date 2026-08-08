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

  it("editing category/keywords/description and an existing step, then saving, PUTs the cleaned payload and exits edit mode", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(playbookFixture(), 200));
      return Promise.resolve(jsonResponse(playbookFixture()));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("p1");

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));

    await userEvent.clear(screen.getByLabelText("Category"));
    await userEvent.type(screen.getByLabelText("Category"), "Malware");
    await userEvent.clear(screen.getByLabelText("Keywords (comma separated)"));
    await userEvent.type(screen.getByLabelText("Keywords (comma separated)"), "malware, ransomware");
    await userEvent.clear(screen.getByLabelText("Description"));
    await userEvent.type(screen.getByLabelText("Description"), "Updated description");

    const stepInput = screen.getByDisplayValue("Check headers");
    await userEvent.clear(stepInput);
    await userEvent.type(stepInput, "Check email headers");

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const putCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "PUT");
      expect(putCall).toBeDefined();
      const body = JSON.parse((putCall![1] as RequestInit).body as string);
      expect(body.category).toBe("Malware");
      expect(body.keywords).toEqual(["malware", "ransomware"]);
      expect(body.description).toBe("Updated description");
      expect(body.steps.detection_analysis).toEqual(["Check email headers"]);
    });

    // Save exits edit mode back to the read-only view.
    expect(screen.queryByLabelText("Category")).not.toBeInTheDocument();
  });

  it("removing a step in edit mode drops its input, and adding-then-leaving-blank omits the phase from the save payload", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(playbookFixture(), 200));
      return Promise.resolve(jsonResponse(playbookFixture()));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("p1");

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.queryByDisplayValue("Check headers")).not.toBeInTheDocument();

    // Add a fresh step but leave it blank -- cleanSteps should filter it and
    // drop the phase entirely from the payload since it has no other steps.
    const addButtons = screen.getAllByRole("button", { name: "+ Add step" });
    await userEvent.click(addButtons[0]);
    await userEvent.type(screen.getByPlaceholderText("Step 1"), "   ");

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const putCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "PUT");
      expect(putCall).toBeDefined();
      const body = JSON.parse((putCall![1] as RequestInit).body as string);
      expect(body.steps.detection_analysis).toBeUndefined();
    });
  });

  it("Cancel while editing an existing playbook reverts the title without saving", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbookFixture())));
    renderDetail("p1");

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    const titleInput = screen.getByDisplayValue("Phishing Response");
    await userEvent.clear(titleInput);
    await userEvent.type(titleInput, "Something else entirely");

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.getByRole("heading", { name: "Phishing Response" })).toBeInTheDocument();
    expect(screen.queryByText("Something else entirely")).not.toBeInTheDocument();
  });

  it("shows an error message when saving an edit to an existing playbook fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
        if (init?.method === "PUT") return Promise.resolve(jsonResponse({ error: "save boom" }, 400));
        return Promise.resolve(jsonResponse(playbookFixture()));
      }),
    );
    renderDetail("p1");

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("save boom")).toBeInTheDocument();
    // Stays in edit mode so the analyst can retry.
    expect(screen.getByLabelText("Category")).toBeInTheDocument();
  });

  it("shows an error message when delete fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
        if (init?.method === "DELETE") return Promise.resolve(jsonResponse({ error: "delete boom" }, 500));
        return Promise.resolve(jsonResponse(playbookFixture()));
      }),
    );
    renderDetail("p1");

    await userEvent.click(await screen.findByRole("button", { name: "Delete" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));

    expect(await screen.findByText("delete boom")).toBeInTheDocument();
    // Falls back out of the confirming state on failure.
    expect(screen.queryByRole("button", { name: "Confirm delete" })).not.toBeInTheDocument();
  });
});
