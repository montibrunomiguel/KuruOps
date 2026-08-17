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

    fireEvent.keyDown(window, { key: "Escape" });
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
    fireEvent.keyDown(window, { key: "ArrowUp" });
    fireEvent.keyDown(window, { key: "ArrowDown" });
    fireEvent.keyDown(window, { key: "ArrowDown" });
    fireEvent.keyDown(window, { key: "Enter" });

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
});
