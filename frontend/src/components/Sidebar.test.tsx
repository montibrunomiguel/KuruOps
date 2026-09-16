import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import i18n from "../i18n";
import { Sidebar } from "./Sidebar";
import { AuthProvider } from "../auth/AuthContext";
import { seedSession, withSession } from "../test/session";

function wrapper({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter>
      <AuthProvider>{children}</AuthProvider>
    </MemoryRouter>
  );
}

function sessionWith(resourceAccess: string[], role = "Analyst", isAdmin = false) {
  seedSession({ id: "1", email: "analyst@test.local", name: "Ana Lyst", role, isAdmin, mustChangePassword: false, resourceAccess })
}

describe("Sidebar", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(new Response("[]", { status: 200, headers: { "content-type": "application/json" } }))));
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
    sessionWith(["alerts", "incidents"], "Admin", true);
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
    sessionWith(["alerts"], "Admin", true);
    render(<Sidebar />, { wrapper });
    expect(await screen.findByText("Ana Lyst")).toBeInTheDocument();
    expect(screen.getByText("Admin")).toBeInTheDocument();
  });

  describe("theme and language toggles", () => {
    afterEach(() => {
      document.documentElement.removeAttribute("data-theme");
      localStorage.removeItem("kuruops.theme");
      void i18n.changeLanguage("en");
      localStorage.removeItem("kuruops.language");
    });

    it("toggling the theme button flips data-theme and persists it", async () => {
      sessionWith(["alerts"]);
      render(<Sidebar />, { wrapper });

      const toggle = await screen.findByRole("button", { name: /modo claro|light mode/i });
      await userEvent.click(toggle);

      expect(document.documentElement.dataset.theme).toBe("light");
      expect(localStorage.getItem("kuruops.theme")).toBe("light");

      await userEvent.click(screen.getByRole("button", { name: /modo escuro|dark mode/i }));
      expect(document.documentElement.dataset.theme).toBe("dark");
    });

    it("clicking EN/PT switches the active language and marks it active", async () => {
      sessionWith(["alerts"]);
      render(<Sidebar />, { wrapper });

      const ptButton = await screen.findByRole("button", { name: "PT" });
      await userEvent.click(ptButton);
      expect(i18n.language).toBe("pt");
      expect(ptButton.dataset.active).toBe("true");

      const enButton = screen.getByRole("button", { name: "EN" });
      await userEvent.click(enButton);
      expect(i18n.language).toBe("en");
      expect(enButton.dataset.active).toBe("true");
    });
  });
});
