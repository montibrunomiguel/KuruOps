import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AdminAuditLogPanel } from "./AdminAuditLogPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <AdminAuditLogPanel />
    </AuthProvider>,
  );
}

const entry1 = {
  id: 2,
  tenantId: "t1",
  area: "smtp-config",
  action: "save",
  actorType: "user",
  actorId: "u1",
  actorName: "Alex Doe",
  data: { from: null, to: { host: "smtp.example.com" } },
  createdAt: "2026-01-02T00:00:00Z",
};
const entry2 = {
  id: 1,
  tenantId: "t1",
  area: "retention",
  action: "save",
  actorType: "user",
  actorId: "u1",
  actorName: "Alex Doe",
  data: { from: null, to: { alertRetentionMonths: 18 } },
  createdAt: "2026-01-01T00:00:00Z",
};

describe("AdminAuditLogPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows the empty state when there are no events", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ events: [], nextCursor: null })));
    renderPanel();

    expect(await screen.findByText("No settings changes recorded yet.")).toBeInTheDocument();
  });

  it("renders each event's area, action, actor, and timestamp", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ events: [entry1, entry2], nextCursor: null })));
    renderPanel();

    expect(await screen.findByText("smtp-config")).toBeInTheDocument();
    expect(screen.getByText("retention")).toBeInTheDocument();
    expect(screen.getAllByText("save")).toHaveLength(2);
    expect(screen.getAllByText("Alex Doe")).toHaveLength(2);
  });

  it("falls back to a placeholder when actorName is blank", async () => {
    const noName = { ...entry1, actorName: "" };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ events: [noName], nextCursor: null })));
    renderPanel();

    expect(await screen.findByText("Unknown user")).toBeInTheDocument();
  });

  it("expands and collapses a row's diff", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ events: [entry1], nextCursor: null })));
    renderPanel();

    const expandButton = await screen.findByRole("button", { name: "Expand" });
    expect(screen.queryByText(/smtp\.example\.com/)).not.toBeInTheDocument();

    await userEvent.click(expandButton);
    expect(screen.getByText(/smtp\.example\.com/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Collapse" }));
    expect(screen.queryByText(/smtp\.example\.com/)).not.toBeInTheDocument();
  });

  it("Load more fetches the next page using the returned cursor and appends results", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (typeof url === "string" && url.includes("beforeCreatedAt")) {
        expect(url).toContain("beforeCreatedAt=2026-01-02T00%3A00%3A00Z");
        expect(url).toContain("beforeId=2");
        return Promise.resolve(jsonResponse({ events: [entry2], nextCursor: null }));
      }
      return Promise.resolve(jsonResponse({ events: [entry1], nextCursor: { createdAt: "2026-01-02T00:00:00Z", id: 2 } }));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    expect(await screen.findByText("smtp-config")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Load more" }));

    expect(await screen.findByText("retention")).toBeInTheDocument();
    expect(screen.getByText("smtp-config")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
  });

  it("a fetch error surfaces the error banner", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "internal error" }, 500)));
    renderPanel();

    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });
});
