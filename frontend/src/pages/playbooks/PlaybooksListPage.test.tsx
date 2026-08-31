import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { PlaybooksListPage } from "./PlaybooksListPage";
import { AuthProvider } from "../../auth/AuthContext";

function wrapper({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter>
      <AuthProvider>{children}</AuthProvider>
    </MemoryRouter>
  );
}

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

const playbooks = [
  { id: "p1", title: "Phishing Response", category: "Phishing", description: "", keywords: ["phishing", "credential"], steps: { detection_analysis: ["Check headers"] } },
  { id: "p2", title: "Ransomware Containment", category: "Ransomware", description: "", keywords: [], steps: {} },
];

describe("PlaybooksListPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders every fetched playbook", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbooks)));
    render(<PlaybooksListPage />, { wrapper });

    expect(await screen.findByText("Phishing Response")).toBeInTheDocument();
    expect(screen.getByText("Ransomware Containment")).toBeInTheDocument();
  });

  it("filters client-side by title, category, or keyword", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbooks)));
    render(<PlaybooksListPage />, { wrapper });
    await screen.findByText("Phishing Response");

    await userEvent.type(screen.getByPlaceholderText(/Search by title/), "ransomware");

    expect(screen.queryByText("Phishing Response")).not.toBeInTheDocument();
    expect(screen.getByText("Ransomware Containment")).toBeInTheDocument();
  });

  it("a keyword match surfaces a playbook whose title doesn't match", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbooks)));
    render(<PlaybooksListPage />, { wrapper });
    await screen.findByText("Phishing Response");

    await userEvent.type(screen.getByPlaceholderText(/Search by title/), "credential");

    expect(screen.getByText("Phishing Response")).toBeInTheDocument();
    expect(screen.queryByText("Ransomware Containment")).not.toBeInTheDocument();
  });

  it("shows the empty state when no playbooks exist", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    render(<PlaybooksListPage />, { wrapper });

    expect(await screen.findByText("No playbook found.")).toBeInTheDocument();
  });

  it("shows the phase count badge per playbook", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbooks)));
    render(<PlaybooksListPage />, { wrapper });
    await screen.findByText("Phishing Response");

    expect(screen.getByText("1 phase")).toBeInTheDocument();
    expect(screen.getByText("0 phases")).toBeInTheDocument();
  });
});

describe("PlaybooksListPage keyboard accessibility", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  // Regression guard for a WCAG 2.1.1 (Level A) failure: the row used to be
  // a `<div onClick={navigate}>` with no tabindex and no role, and it was
  // the ONLY way to open a playbook -- so a keyboard-only or screen-reader
  // user could create and search playbooks but never open one.
  it("exposes each playbook as a real link, reachable by keyboard", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbooks)));
    render(<PlaybooksListPage />, { wrapper });

    const link = await screen.findByRole("link", { name: "Phishing Response" });
    expect(link).toHaveAttribute("href", "/playbooks/p1");
  });

  it("puts the playbook link in the page's tab order", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(playbooks)));
    render(<PlaybooksListPage />, { wrapper });
    await screen.findByRole("link", { name: "Phishing Response" });

    // A <div onClick> renders no element matching this selector at all --
    // which is exactly how the bug went unnoticed.
    const focusable = document.querySelectorAll(
      'a[href],button,input,select,textarea,[tabindex]:not([tabindex="-1"])',
    );
    const hrefs = [...focusable].map((e) => e.getAttribute("href")).filter(Boolean);
    expect(hrefs).toContain("/playbooks/p1");
  });
});
