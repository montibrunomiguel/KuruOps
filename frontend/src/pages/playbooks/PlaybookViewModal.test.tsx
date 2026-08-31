import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { PlaybookViewModal } from "./PlaybookViewModal";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function playbookFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "p1", title: "Ransomware Response", category: "Ransomware", description: "Contain and eradicate.",
    keywords: [], alertNamePattern: "", isDefault: false,
    steps: {
      containment: [
        { id: "s1", text: "Isolate host", webhookUrl: "https://hooks.example/isolate", webhookPayloadTemplate: "" },
        { id: "s2", text: "Notify legal" },
      ],
    },
    ...overrides,
  };
}

function renderModal(props: Partial<{ playbookId: string; alertId: string; onClose: () => void }> = {}) {
  const onClose = props.onClose ?? vi.fn();
  render(
    <MemoryRouter>
      <AuthProvider>
        <PlaybookViewModal playbookId={props.playbookId ?? "p1"} alertId={props.alertId ?? "a1"} onClose={onClose} />
      </AuthProvider>
    </MemoryRouter>,
  );
  return onClose;
}

describe("PlaybookViewModal", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("fetches and renders the playbook's title, category, description, and steps by phase", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbookFixture())));
    renderModal();

    expect(await screen.findByRole("heading", { name: "Ransomware Response" })).toBeInTheDocument();
    expect(screen.getByText("Ransomware")).toBeInTheDocument();
    expect(screen.getByText("Contain and eradicate.")).toBeInTheDocument();
    expect(screen.getByText("Isolate host")).toBeInTheDocument();
    expect(screen.getByText("Notify legal")).toBeInTheDocument();
  });

  it("never renders a 'New'-phase steps section, even if the fetched playbook has one", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(playbookFixture({ steps: { new: [{ id: "s0", text: "Should never show" }], containment: [{ id: "s1", text: "Isolate host" }] } })),
      ),
    );
    renderModal();

    await screen.findByText("Isolate host");
    expect(screen.queryByText("Should never show")).not.toBeInTheDocument();
  });

  it("shows an error banner when the playbook fails to load", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "load boom" }, 500)));
    renderModal();

    expect(await screen.findByText("load boom")).toBeInTheDocument();
  });

  it("only shows the 'Run automation' button on steps with a webhook configured", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbookFixture())));
    renderModal();

    await screen.findByText("Isolate host");
    expect(screen.getAllByRole("button", { name: "Run automation" })).toHaveLength(1);
  });

  it("clicking 'Run automation' POSTs the alertId to the step's trigger endpoint and shows success", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(playbookFixture()));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderModal({ alertId: "a42" });

    await userEvent.click(await screen.findByRole("button", { name: "Run automation" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/playbooks/steps/s1/trigger",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ alertId: "a42" }) }),
      ),
    );
    expect(await screen.findByText("Automation triggered successfully.")).toBeInTheDocument();
  });

  it("shows an error message inline when triggering fails", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ error: "webhook unreachable" }, 502));
      return Promise.resolve(jsonResponse(playbookFixture()));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderModal();

    await userEvent.click(await screen.findByRole("button", { name: "Run automation" }));

    expect(await screen.findByText("Failed to trigger automation: webhook unreachable")).toBeInTheDocument();
  });

  it("the footer Close button calls onClose", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbookFixture())));
    const onClose = renderModal();

    // Both the header "×" and the footer button share the accessible name
    // "Close" (the × via aria-label) -- the footer one is last in document order.
    const closeButtons = await screen.findAllByRole("button", { name: "Close" });
    await userEvent.click(closeButtons[closeButtons.length - 1]);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("links to the full playbook page for editing", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbookFixture())));
    renderModal();

    const editLink = await screen.findByRole("link", { name: "Edit playbook" });
    expect(editLink).toHaveAttribute("href", "/playbooks/p1");
  });
});
