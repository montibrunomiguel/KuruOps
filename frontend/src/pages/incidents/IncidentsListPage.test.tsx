import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { IncidentsListPage } from "./IncidentsListPage";
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
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([incidentFixture()])));
    render(<IncidentsListPage />, { wrapper });

    expect(await screen.findByText("Ransomware suspected")).toBeInTheDocument();
  });

  it("flags SLA-breached incidents distinctly", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([incidentFixture({ slaBreached: true })])));
    render(<IncidentsListPage />, { wrapper });

    expect(await screen.findByText("SLA breached")).toBeInTheDocument();
  });

  it("shows the empty state with no results", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    render(<IncidentsListPage />, { wrapper });

    expect(await screen.findByText("No incidents found for the current filters.")).toBeInTheDocument();
  });

  it("opens the create form, and a missing title blocks submission via the required attribute", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
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
    vi.stubGlobal("fetch", fetchMock);
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
    vi.stubGlobal("fetch", fetchMock);
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
    vi.stubGlobal("fetch", fetchMock);
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
});
