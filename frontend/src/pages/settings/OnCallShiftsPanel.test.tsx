import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { OnCallShiftsPanel } from "./OnCallShiftsPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function shiftFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "s1", tenantId: "t1", userId: "u1", userName: "Marina Alves",
    weekday: 1, startMinute: 540, endMinute: 1020, createdAt: "2026-01-01T00:00:00Z", ...overrides,
  };
}

function routeFetch(schedule: unknown) {
  return vi.fn().mockImplementation((url: string) => {
    if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }]));
    if (url.includes("/api/v1/settings/on-call-shifts")) return Promise.resolve(jsonResponse(schedule));
    return Promise.resolve(jsonResponse({}));
  });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <OnCallShiftsPanel />
    </AuthProvider>,
  );
}

describe("OnCallShiftsPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("lists existing shifts with weekday, time range, and analyst name", async () => {
    vi.stubGlobal("fetch", routeFetch({ timezone: "UTC", shifts: [shiftFixture()] }));
    renderPanel();

    expect(await screen.findByText("Marina Alves")).toBeInTheDocument();
    expect(screen.getByText(/Monday/)).toBeInTheDocument();
    expect(screen.getByText(/09:00–17:00/)).toBeInTheDocument();
  });

  it("shows the empty state with none configured", async () => {
    vi.stubGlobal("fetch", routeFetch({ timezone: "UTC", shifts: [] }));
    renderPanel();

    expect(await screen.findByText("No shifts configured -- new alerts stay unassigned.")).toBeInTheDocument();
  });

  it("creating a shift posts the resolved userId/weekday/minutes", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(shiftFixture(), 201));
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }]));
      if (url.includes("/api/v1/settings/on-call-shifts")) return Promise.resolve(jsonResponse({ timezone: "UTC", shifts: [] }));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No shifts configured -- new alerts stay unassigned.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Shift" }));
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/on-call-shifts",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ userId: "u1", weekday: 1, startMinute: 540, endMinute: 1020 }) }),
      ),
    );
  });

  it("deleting requires an inline confirm click, and Cancel backs out without deleting", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/settings/on-call-shifts/s1")) return Promise.resolve(new Response(null, { status: 204 }));
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }]));
      if (url.includes("/api/v1/settings/on-call-shifts")) return Promise.resolve(jsonResponse({ timezone: "UTC", shifts: [shiftFixture()] }));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Marina Alves");

    // Clicking Delete the first time only reveals an inline confirm/cancel
    // pair -- no native confirm() dialog, which some embedded browser
    // contexts silently auto-dismiss with no visible prompt at all.
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/on-call-shifts/s1", expect.anything());
    expect(screen.getByText("Remove this shift?")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText("Remove this shift?")).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/on-call-shifts/s1", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/on-call-shifts/s1", expect.objectContaining({ method: "DELETE" })),
    );
  });

  it("shows an error instead of failing silently when delete fails", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/v1/settings/on-call-shifts/s1")) return Promise.resolve(jsonResponse({ error: "shift not found" }, 404));
      if (url.includes("/api/v1/users/directory")) return Promise.resolve(jsonResponse([{ id: "u1", name: "Marina Alves" }]));
      if (url.includes("/api/v1/settings/on-call-shifts")) return Promise.resolve(jsonResponse({ timezone: "UTC", shifts: [shiftFixture()] }));
      return Promise.resolve(jsonResponse({}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Marina Alves");

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));

    expect(await screen.findByText("shift not found")).toBeInTheDocument();
  });

  it("saving a changed timezone PUTs it", async () => {
    const fetchMock = routeFetch({ timezone: "UTC", shifts: [] });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const tzInput = await screen.findByLabelText("Timezone (IANA name, e.g. America/Sao_Paulo)");
    await userEvent.clear(tzInput);
    await userEvent.type(tzInput, "America/Sao_Paulo");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/on-call-shifts/timezone",
        expect.objectContaining({ method: "PUT", body: JSON.stringify({ timezone: "America/Sao_Paulo" }) }),
      ),
    );
  });
});
