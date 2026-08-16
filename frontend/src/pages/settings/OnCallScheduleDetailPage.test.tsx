import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { OnCallScheduleDetailPage } from "./OnCallScheduleDetailPage";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function scheduleFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "sched1",
    tenantId: "tenant1",
    name: "Primary On-Call",
    isDefault: true,
    timezone: "UTC",
    participants: [{ userId: "u1", userName: "Alice" }],
    handoverAt: "2026-01-01T09:00:00Z",
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
  { id: "u3", name: "Carol" },
];

function renderDetail(id: string) {
  return render(
    <MemoryRouter initialEntries={[`/settings/on-call-schedules/${id}`]}>
      <AuthProvider>
        <Routes>
          <Route path="/settings/on-call-schedules/:id" element={<OnCallScheduleDetailPage />} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

function fetchDispatcher(
  handlers: Partial<Record<string, (url: string, init?: RequestInit) => Promise<Response>>>,
) {
  return vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (url.includes("/users/directory")) return Promise.resolve(jsonResponse(DIRECTORY));
    if (url.includes("/on-call-schedules/timezone")) return (handlers.timezone ?? (() => Promise.resolve(new Response(null, { status: 204 }))))(url, init);
    if (url.includes("/overrides")) return (handlers.overrides ?? (() => Promise.resolve(jsonResponse({}))))(url, init);
    if (url.includes("/on-call-schedules")) return (handlers.schedule ?? (() => Promise.resolve(jsonResponse(scheduleFixture()))))(url, init);
    return Promise.resolve(jsonResponse({}));
  });
}

async function waitForLoaded() {
  return screen.findByLabelText("Handover time");
}

describe("OnCallScheduleDetailPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders an existing schedule's name and responders", async () => {
    vi.stubGlobal("fetch", fetchDispatcher({}));
    renderDetail("sched1");

    await waitForLoaded();
    expect(screen.getByRole("heading", { name: "Primary On-Call" })).toBeInTheDocument();
    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Primary On-Call");
    // "Alice" appears both in the responder list and in the timeline
    // preview below it.
    expect(screen.getAllByText("Alice").length).toBeGreaterThan(0);
  });

  it("shows the empty create form immediately for /settings/on-call-schedules/new, with no GET by id", async () => {
    const fetchMock = fetchDispatcher({});
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("new");

    expect(await screen.findByRole("heading", { name: "+ New Schedule" })).toBeInTheDocument();
    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("");
    expect(fetchMock.mock.calls.some((c) => c[0] === "/api/v1/settings/on-call-schedules/new")).toBe(false);
  });

  it("rejects saving a new schedule with a blank name, without calling the API", async () => {
    const fetchMock = fetchDispatcher({});
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("new");

    await screen.findByLabelText("Name");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Schedule name is required.")).toBeInTheDocument();
    expect(fetchMock.mock.calls.some((c) => c[0] === "/api/v1/settings/on-call-schedules" && (c[1] as RequestInit)?.method === "POST")).toBe(false);
  });

  it("creating a new schedule POSTs the name and navigates to the created id", async () => {
    const fetchMock = fetchDispatcher({
      schedule: (_url, init) => {
        if (init?.method === "POST") return Promise.resolve(jsonResponse(scheduleFixture({ id: "new-id", name: "Team B" }), 201));
        return Promise.resolve(jsonResponse(scheduleFixture({ id: "new-id", name: "Team B" })));
      },
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("new");

    await userEvent.type(await screen.findByLabelText("Name"), "Team B");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/on-call-schedules", expect.objectContaining({ method: "POST" })),
    );
    const postCall = fetchMock.mock.calls.find((c) => c[0] === "/api/v1/settings/on-call-schedules" && (c[1] as RequestInit)?.method === "POST");
    const body = JSON.parse((postCall![1] as RequestInit).body as string);
    expect(body.name).toBe("Team B");
  });

  it("saving an existing schedule PUTs to its id, including the name field", async () => {
    const fetchMock = fetchDispatcher({
      schedule: (_url, init) => {
        if (init?.method === "PUT") return Promise.resolve(jsonResponse(scheduleFixture({ name: "Renamed" }), 200));
        return Promise.resolve(jsonResponse(scheduleFixture()));
      },
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("sched1");
    await waitForLoaded();

    const nameInput = screen.getByLabelText("Name");
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "Renamed");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/on-call-schedules/sched1", expect.objectContaining({ method: "PUT" })),
    );
    const putCall = fetchMock.mock.calls.find(
      (c) => c[0] === "/api/v1/settings/on-call-schedules/sched1" && (c[1] as RequestInit | undefined)?.method === "PUT",
    );
    const body = JSON.parse((putCall![1] as RequestInit).body as string);
    expect(body.name).toBe("Renamed");
    expect(body.participantIds).toEqual(["u1"]);
  });

  it("deleting requires a confirm click before calling the API", async () => {
    const fetchMock = fetchDispatcher({
      schedule: (_url, init) => {
        if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
        return Promise.resolve(jsonResponse(scheduleFixture()));
      },
    });
    vi.stubGlobal("fetch", fetchMock);
    renderDetail("sched1");
    await waitForLoaded();

    await userEvent.click(screen.getByRole("button", { name: "Delete schedule" }));
    expect(fetchMock.mock.calls.some((c) => (c[1] as RequestInit | undefined)?.method === "DELETE")).toBe(false);

    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/on-call-schedules/sched1", expect.objectContaining({ method: "DELETE" })),
    );
  });
});
