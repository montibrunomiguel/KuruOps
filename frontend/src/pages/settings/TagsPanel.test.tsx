import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TagsPanel } from "./TagsPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function tagFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return { id: "t1", name: "phishing", color: "#ff0000", createdAt: "2026-01-01T00:00:00Z", ...overrides };
}

function renderPanel() {
  return render(
    <AuthProvider>
      <TagsPanel />
    </AuthProvider>,
  );
}

describe("TagsPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("lists existing tags", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([tagFixture()])));
    renderPanel();

    expect(await screen.findByText("phishing")).toBeInTheDocument();
  });

  it("shows the empty state with none created", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderPanel();

    expect(await screen.findByText("No tags registered yet.")).toBeInTheDocument();
  });

  it("creating a tag posts name+color", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(tagFixture(), 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No tags registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Tag" }));
    await userEvent.type(screen.getByLabelText("Name"), "vpn");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/tags", expect.objectContaining({ method: "POST" })),
    );
  });

  it("deleting requires an inline confirm click, and Cancel backs out without deleting", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/settings/tags/")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([tagFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("phishing");

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/tags/t1", expect.anything());
    expect(screen.getByText(/Delete this tag\?/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText(/Delete this tag\?/)).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/tags/t1", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/tags/t1", expect.objectContaining({ method: "DELETE" })),
    );
  });

  it("shows an error instead of failing silently when delete fails", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/settings/tags/")) return Promise.resolve(jsonResponse({ error: "tag not found" }, 404));
      return Promise.resolve(jsonResponse([tagFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("phishing");

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));

    expect(await screen.findByText("tag not found")).toBeInTheDocument();
  });

  it("shows a validation error from the API", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ error: "tag name is required" }, 400));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No tags registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Tag" }));
    const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
    nameInput.removeAttribute("required");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("tag name is required")).toBeInTheDocument();
  });
});
