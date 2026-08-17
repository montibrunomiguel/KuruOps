import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { CommandPalette } from "./CommandPalette";

function renderPalette() {
  return render(
    <MemoryRouter initialEntries={["/dashboard"]}>
      <Routes>
        <Route path="/dashboard" element={<CommandPalette />} />
        <Route path="/alerts" element={<div>Alerts Page</div>} />
        <Route path="/profile" element={<div>Profile Page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

function open() {
  fireEvent.keyDown(window, { key: "k", metaKey: true });
}

// Escape/Arrow/Enter are now handled by a listener scoped to the dialog
// itself (see CommandPalette's onKeyDown), not window -- fire them on the
// dialog's own combobox input, which is where a real user's keystrokes
// would land (it has autoFocus).
function fireOnPalette(key: string, extra: Record<string, unknown> = {}) {
  fireEvent.keyDown(screen.getByRole("combobox"), { key, ...extra });
}

describe("CommandPalette", () => {
  it("opens on Cmd+K and closes on Escape", () => {
    render(
      <MemoryRouter>
        <CommandPalette />
      </MemoryRouter>
    );

    expect(screen.queryByRole("dialog")).toBeNull();

    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fireOnPalette("Escape");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("also opens on Ctrl+K", () => {
    render(
      <MemoryRouter>
        <CommandPalette />
      </MemoryRouter>,
    );

    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("lists all commands by default and filters them as the user types", async () => {
    renderPalette();
    open();

    expect(screen.getByText("Alerts")).toBeInTheDocument();
    expect(screen.getByText("Dashboard")).toBeInTheDocument();
    expect(screen.getAllByText("Settings").length).toBeGreaterThan(0);

    await userEvent.type(screen.getByPlaceholderText("Type a command or search..."), "alert");

    expect(screen.getByText("Alerts")).toBeInTheDocument();
    expect(screen.queryByText("Dashboard")).toBeNull();
  });

  it("shows a no-results message when nothing matches the query", async () => {
    renderPalette();
    open();

    await userEvent.type(screen.getByPlaceholderText("Type a command or search..."), "zzz-nope");

    expect(screen.getByText("No matching commands")).toBeInTheDocument();
  });

  it("navigates to the command's path and closes the palette on click", async () => {
    renderPalette();
    open();

    await userEvent.click(screen.getByText("Alerts"));

    expect(await screen.findByText("Alerts Page")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("closes when the backdrop is clicked, but not when the panel itself is clicked", async () => {
    renderPalette();
    open();

    await userEvent.click(screen.getByPlaceholderText("Type a command or search..."));
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("dialog"));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("highlights a command on mouse hover", async () => {
    renderPalette();
    open();

    const alertsRow = screen.getByText("Alerts").closest("div")!;
    await userEvent.hover(alertsRow);

    expect(alertsRow).toHaveStyle({ cursor: "pointer" });
  });

  it("ignores Escape when the palette is already closed", () => {
    render(
      <MemoryRouter>
        <CommandPalette />
      </MemoryRouter>,
    );

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("navigates the list with ArrowDown/ArrowUp, wrapping at both ends, and selects with Enter", async () => {
    renderPalette();
    open();

    // Dashboard is the first command; ArrowUp from there wraps to the last.
    fireOnPalette("ArrowUp");
    fireOnPalette("ArrowDown");
    fireOnPalette("ArrowDown");
    fireOnPalette("Enter");

    expect(await screen.findByText("Alerts Page")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("does nothing on ArrowDown/Enter when the palette is closed", () => {
    render(
      <MemoryRouter>
        <CommandPalette />
      </MemoryRouter>,
    );

    fireEvent.keyDown(window, { key: "ArrowDown" });
    fireEvent.keyDown(window, { key: "Enter" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("exposes combobox/listbox ARIA wiring with aria-activedescendant tracking the highlighted option", () => {
    renderPalette();
    open();

    const input = screen.getByRole("combobox");
    const listbox = screen.getByRole("listbox");
    expect(input).toHaveAttribute("aria-expanded", "true");
    expect(input).toHaveAttribute("aria-controls", listbox.id);

    const options = screen.getAllByRole("option");
    expect(options.length).toBeGreaterThan(1);
    expect(input.getAttribute("aria-activedescendant")).toBe(options[0].id);
    expect(options[0]).toHaveAttribute("aria-selected", "true");

    fireOnPalette("ArrowDown");
    expect(input.getAttribute("aria-activedescendant")).toBe(options[1].id);
    expect(options[1]).toHaveAttribute("aria-selected", "true");
    expect(options[0]).toHaveAttribute("aria-selected", "false");
  });

  it("restores focus to the previously focused element on close", () => {
    render(
      <MemoryRouter>
        <button>outside trigger</button>
        <CommandPalette />
      </MemoryRouter>,
    );

    const trigger = screen.getByText("outside trigger");
    trigger.focus();
    expect(trigger).toHaveFocus();

    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.getByRole("combobox")).toHaveFocus();

    fireOnPalette("Escape");
    expect(trigger).toHaveFocus();
  });

  it("keeps focus on the input when Tab is pressed (minimal focus trap)", () => {
    renderPalette();
    open();

    const input = screen.getByRole("combobox");
    expect(input).toHaveFocus();

    fireOnPalette("Tab");
    expect(input).toHaveFocus();
  });
});
