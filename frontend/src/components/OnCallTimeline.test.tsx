import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { OnCallTimeline } from "./OnCallTimeline";
import { AuthProvider } from "../auth/AuthContext";
import type { OnCallSchedule } from "../types/onCallSchedule";
import { personColor, personTextColor } from "../lib/personColor";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function scheduleFixture(overrides: Partial<OnCallSchedule> = {}): OnCallSchedule {
  return {
    id: "sched1",
    tenantId: "tenant1",
    name: "Primary On-Call",
    isDefault: true,
    timezone: "UTC",
    participants: [{ userId: "u1", userName: "Alice" }],
    handoverAt: "2026-01-01T00:00:00Z",
    periodDays: 7,
    concurrentShifts: 1,
    workingHoursMode: "all_day",
    workingHours: [],
    overrides: [],
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

const DIRECTORY = [
  { id: "u1", name: "Alice" },
  { id: "u2", name: "Bob" },
];

function renderTimeline(schedule: OnCallSchedule, onOverrideChange = vi.fn()) {
  render(
    <AuthProvider>
      <OnCallTimeline schedule={schedule} directory={DIRECTORY} onOverrideChange={onOverrideChange} />
    </AuthProvider>,
  );
  return onOverrideChange;
}

// isoDate mirrors OnCallTimeline's own local-date formatting -- used only
// to compute the expected override date for whichever day button the test
// happens to click, without needing to fake the system clock (the default
// visible range is always "the week containing today", so real time works
// fine here).
function isoDate(d: Date): string {
  const y = d.getFullYear();
  const m = (d.getMonth() + 1).toString().padStart(2, "0");
  const day = d.getDate().toString().padStart(2, "0");
  return `${y}-${m}-${day}`;
}

describe("OnCallTimeline", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders a colored bar for the sole participant on call", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({})));
    renderTimeline(scheduleFixture());

    expect(screen.getAllByText("Alice").length).toBeGreaterThan(0);
  });

  it("shows the empty state when no responders are configured", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({})));
    renderTimeline(scheduleFixture({ participants: [] }));

    expect(screen.getByText(/Add responders above/)).toBeInTheDocument();
  });

  it("renders the now-line since today falls within the default visible week", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({})));
    renderTimeline(scheduleFixture());

    expect(screen.getByTestId("oncall-now-line")).toBeInTheDocument();
  });

  it("clicking a day opens the override popup, and assigning posts the override", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/overrides") && init?.method === "POST") {
        return Promise.resolve(jsonResponse({ id: "ov1", date: "2026-01-14", userId: "u2", userName: "Bob" }, 201));
      }
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    const onOverrideChange = renderTimeline(scheduleFixture());

    const today = new Date();
    const expectedDate = isoDate(today);
    await user.click(screen.getByRole("button", { name: String(today.getDate()) }));
    expect(screen.getByText(/Override --/)).toBeInTheDocument();

    const select = screen.getByLabelText("Analyst") as HTMLSelectElement;
    await user.selectOptions(select, "u2");
    await user.click(screen.getByRole("button", { name: "Assign" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/on-call-schedules/sched1/overrides",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "POST");
    const body = JSON.parse((postCall![1] as RequestInit).body as string);
    expect(body).toEqual({ userId: "u2", date: expectedDate });
    expect(onOverrideChange).toHaveBeenCalled();
  });

  it("an existing override for today offers Remove, which asks for confirmation before deleting", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/overrides/ov1") && init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    const today = new Date();
    const schedule = scheduleFixture({
      overrides: [{ id: "ov1", date: isoDate(today), userId: "u2", userName: "Bob", createdAt: "2026-01-01T00:00:00Z" }],
    });
    const onOverrideChange = renderTimeline(schedule);

    await user.click(screen.getByRole("button", { name: String(today.getDate()) }));
    await user.click(screen.getByRole("button", { name: "Remove override" }));

    // Clicking "Remove override" alone must not delete anything yet -- it
    // only reveals the inline confirm/cancel step (see useConfirm).
    expect(fetchMock).not.toHaveBeenCalledWith(
      "/api/v1/settings/on-call-schedules/sched1/overrides/ov1",
      expect.objectContaining({ method: "DELETE" }),
    );
    expect(screen.getByText("Remove this override?")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/on-call-schedules/sched1/overrides/ov1",
        expect.objectContaining({ method: "DELETE" }),
      ),
    );
    expect(onOverrideChange).toHaveBeenCalled();
  });

  it("cancelling the remove confirmation leaves the override untouched", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}));
    vi.stubGlobal("fetch", fetchMock);
    const today = new Date();
    const schedule = scheduleFixture({
      overrides: [{ id: "ov1", date: isoDate(today), userId: "u2", userName: "Bob", createdAt: "2026-01-01T00:00:00Z" }],
    });
    renderTimeline(schedule);

    await user.click(screen.getByRole("button", { name: String(today.getDate()) }));
    await user.click(screen.getByRole("button", { name: "Remove override" }));
    // Two "Cancel" buttons exist in the popup once confirming (the inline
    // confirm's Cancel and the form's own Cancel/close) -- the inline one
    // is the first, right next to Confirm.
    await user.click(screen.getAllByRole("button", { name: "Cancel" })[0]);

    expect(screen.queryByText("Remove this override?")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove override" })).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith(
      expect.stringContaining("/overrides/ov1"),
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("renders the on-call bar's text color from personTextColor, not hardcoded white", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({})));
    renderTimeline(scheduleFixture());

    const bar = screen.getAllByText("Alice").find((el) => el.title === "Alice");
    expect(bar).toBeDefined();
    const expectedTextColor = personTextColor(personColor("u1"));
    const expectedRgb = expectedTextColor === "#000" ? "rgb(0, 0, 0)" : "rgb(255, 255, 255)";
    expect(bar!.style.color).toBe(expectedRgb);
    // The regression this guards against: personColor("u1") is a yellow
    // that fails WCAG AA badly with white text (1.59:1, far below the
    // 4.5:1 minimum) -- confirm this fixture actually exercises the fix
    // rather than coincidentally landing on the one palette color (the
    // purple) where white was always fine.
    expect(expectedTextColor).toBe("#000");
  });
});
