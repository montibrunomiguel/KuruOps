import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { IOCsModal } from "./IOCsModal";
import { AuthProvider } from "../auth/AuthContext";
import type { IOC } from "../types/incidents";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderModal(onClose: () => void = vi.fn()) {
  return render(
    <AuthProvider>
      <IOCsModal incidentId="inc-1" onClose={onClose} />
    </AuthProvider>,
  );
}

describe("IOCsModal", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows an empty-state message when the incident has no IOCs yet", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderModal();

    expect(await screen.findByText(/No IOCs recorded yet/)).toBeInTheDocument();
  });

  it("renders an existing IOC's type, value, description, and identified-by line", async () => {
    const iocs: IOC[] = [
      {
        id: "ioc-1",
        incidentId: "inc-1",
        tenantId: "t1",
        type: "ip_address",
        value: "203.0.113.42",
        description: "C2 beacon traffic",
        identifiedAt: "2026-08-20T10:00:00Z",
        createdBy: "u1",
        createdByName: "Analyst One",
        createdAt: "2026-08-20T10:05:00Z",
      },
    ];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(iocs)));
    renderModal();

    expect(await screen.findByText("203.0.113.42")).toBeInTheDocument();
    expect(screen.getByText("C2 beacon traffic")).toBeInTheDocument();
    expect(screen.getByText("IP Address")).toBeInTheDocument();
    expect(screen.getByText(/Analyst One/)).toBeInTheDocument();
  });

  it("the add form is hidden until 'Add IOC' is clicked, and submitting it posts to the iocs endpoint", async () => {
    let posted = false;
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST" && url === "/api/v1/incidents/inc-1/iocs") {
        posted = true;
        return Promise.resolve(jsonResponse({ id: "ioc-2" }, 201));
      }
      if (!posted) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(
        jsonResponse([
          {
            id: "ioc-2",
            incidentId: "inc-1",
            tenantId: "t1",
            type: "domain_name",
            value: "evil.example.com",
            description: "",
            identifiedAt: "2026-08-28T00:00:00Z",
            createdBy: "u1",
            createdByName: "Analyst One",
            createdAt: "2026-08-28T00:00:00Z",
          },
        ] satisfies IOC[]),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    renderModal();

    await screen.findByText(/No IOCs recorded yet/);
    expect(screen.queryByLabelText("Value")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "+ Add IOC" }));
    await userEvent.selectOptions(screen.getByLabelText("Type"), "domain_name");
    await userEvent.type(screen.getByLabelText("Value"), "evil.example.com");
    await userEvent.click(screen.getByRole("button", { name: "Add IOC" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents/inc-1/iocs",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    const [, init] = fetchMock.mock.calls.find(([, i]) => i?.method === "POST")!;
    const sent = JSON.parse((init as RequestInit).body as string);
    expect(sent.type).toBe("domain_name");
    expect(sent.value).toBe("evil.example.com");

    expect(await screen.findByText("evil.example.com")).toBeInTheDocument();
  });

  it("the value field is required, blocking submission without ever calling the API", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    renderModal();

    await screen.findByText(/No IOCs recorded yet/);
    await userEvent.click(screen.getByRole("button", { name: "+ Add IOC" }));
    const valueInput = screen.getByLabelText("Value") as HTMLInputElement;
    expect(valueInput).toBeRequired();

    await userEvent.click(screen.getByRole("button", { name: "Add IOC" }));

    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/incidents/inc-1/iocs", expect.objectContaining({ method: "POST" }));
  });
});
