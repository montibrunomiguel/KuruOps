import { useState, type FormEvent } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Modal } from "./Modal";

describe("Modal", () => {
  it("renders as a dialog with aria-modal", () => {
    render(
      <Modal onClose={vi.fn()}>
        <button>Only button</button>
      </Modal>,
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
  });

  it("calls onClose when the overlay (outside the panel) is clicked", async () => {
    const onClose = vi.fn();
    render(
      <Modal onClose={onClose}>
        <button>Inside</button>
      </Modal>,
    );
    // The overlay is the dialog's parent -- click it directly, not the panel.
    await userEvent.click(screen.getByRole("dialog").parentElement!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not call onClose when content inside the panel is clicked", async () => {
    const onClose = vi.fn();
    render(
      <Modal onClose={onClose}>
        <button>Inside</button>
      </Modal>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Inside" }));
    expect(onClose).not.toHaveBeenCalled();
  });

  it("calls onClose when Escape is pressed", async () => {
    const onClose = vi.fn();
    render(
      <Modal onClose={onClose}>
        <button>Inside</button>
      </Modal>,
    );
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("moves initial focus to the first focusable element inside the panel", async () => {
    render(
      <Modal onClose={vi.fn()}>
        <button>First</button>
        <button>Second</button>
      </Modal>,
    );
    expect(await screen.findByRole("button", { name: "First" })).toHaveFocus();
  });

  it("Tab from the last focusable element cycles back to the first (focus trap)", async () => {
    render(
      <Modal onClose={vi.fn()}>
        <button>First</button>
        <button>Second</button>
      </Modal>,
    );
    const first = screen.getByRole("button", { name: "First" });
    const second = screen.getByRole("button", { name: "Second" });
    expect(first).toHaveFocus();

    second.focus();
    await userEvent.tab();
    expect(first).toHaveFocus();
  });

  it("Shift+Tab from the first focusable element cycles to the last (focus trap)", async () => {
    render(
      <Modal onClose={vi.fn()}>
        <button>First</button>
        <button>Second</button>
      </Modal>,
    );
    const first = screen.getByRole("button", { name: "First" });
    const second = screen.getByRole("button", { name: "Second" });
    expect(first).toHaveFocus();

    await userEvent.tab({ shift: true });
    expect(second).toHaveFocus();
  });

  it("restores focus to whatever had it before the modal opened, once closed", () => {
    function Harness() {
      const [open, setOpen] = useState(true);
      return (
        <div>
          <button id="trigger" onClick={() => setOpen(true)}>
            Open
          </button>
          {open && (
            <Modal onClose={() => setOpen(false)}>
              <button>Inside</button>
            </Modal>
          )}
        </div>
      );
    }
    document.body.innerHTML = "";
    const triggerButton = document.createElement("button");
    triggerButton.textContent = "external trigger";
    document.body.appendChild(triggerButton);
    triggerButton.focus();
    expect(triggerButton).toHaveFocus();

    const { unmount } = render(<Harness />);
    // Opening the modal must have moved focus away from the external trigger.
    expect(triggerButton).not.toHaveFocus();
    unmount();
    expect(triggerButton).toHaveFocus();
    triggerButton.remove();
  });

  it("renders the panel as a <form> and submits it when as=\"form\"", async () => {
    const onSubmit = vi.fn((e: FormEvent) => e.preventDefault());
    render(
      <Modal onClose={vi.fn()} as="form" onSubmit={onSubmit}>
        <button type="submit">Submit</button>
      </Modal>,
    );
    expect(screen.getByRole("dialog").tagName).toBe("FORM");
    await userEvent.click(screen.getByRole("button", { name: "Submit" }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("appends a custom className to the base modal class", () => {
    render(
      <Modal onClose={vi.fn()} className="custom-class">
        <button>Inside</button>
      </Modal>,
    );
    expect(screen.getByRole("dialog")).toHaveClass("modal", "custom-class");
  });
});
