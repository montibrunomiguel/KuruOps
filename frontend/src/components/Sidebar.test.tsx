import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { Sidebar } from "./Sidebar";
import { AuthProvider } from "../auth/AuthContext";

function wrapper({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter>
      <AuthProvider>{children}</AuthProvider>
    </MemoryRouter>
  );
}

function sessionWith(resourceAccess: string[], role = "analyst") {
  localStorage.setItem(
    "argusops.session",
    JSON.stringify({
      token: "tok",
      user: { id: "1", email: "analyst@test.local", name: "Ana Lyst", role, mustChangePassword: false, resourceAccess },
    }),
  );
}

describe("Sidebar", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200, headers: { "content-type": "application/json" } })));
  });

  it("shows only Alerts + Follow-up-eligible Dashboard for an alerts-only analyst, no Incidents/Settings", async () => {
    sessionWith(["alerts"]);
    render(<Sidebar />, { wrapper });

    expect(await screen.findByText("Dashboard")).toBeInTheDocument();
    expect(screen.getByText("Alerts")).toBeInTheDocument();
    expect(screen.queryByText("Incidents")).not.toBeInTheDocument();
    expect(screen.getByText("Playbooks")).toBeInTheDocument();
    expect(screen.queryByText("Settings")).not.toBeInTheDocument();
  });

  it("shows Incidents when granted, and Settings only for admins", async () => {
    sessionWith(["alerts", "incidents"], "admin");
    render(<Sidebar />, { wrapper });

    expect(await screen.findByText("Incidents")).toBeInTheDocument();
    expect(screen.getByText("Settings")).toBeInTheDocument();
  });

  it("hides Dashboard entirely when the user has none of alerts/incidents/followup", async () => {
    sessionWith([]);
    render(<Sidebar />, { wrapper });

    await waitFor(() => expect(screen.getByText("Playbooks")).toBeInTheDocument());
    expect(screen.queryByText("Dashboard")).not.toBeInTheDocument();
  });

  it("shows the logged-in user's name and role", async () => {
    sessionWith(["alerts"], "admin");
    render(<Sidebar />, { wrapper });
    expect(await screen.findByText("Ana Lyst")).toBeInTheDocument();
    expect(screen.getByText("Admin")).toBeInTheDocument();
  });
});
