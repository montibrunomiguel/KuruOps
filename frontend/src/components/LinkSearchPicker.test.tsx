import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LinkSearchPicker } from "./LinkSearchPicker";
import type { Alert } from "../types/alerts";

function alertFixture(overrides: Partial<Alert> = {}): Alert {
  return {
    id: "a1b2c3d4-0000-0000-0000-000000000000",
    tenantId: "t1",
    title: "Suspicious login",
    source: "wazuh",
    severity: "high",
    status: "open",
    receivedAt: "2026-01-01T00:00:00Z",
    tags: [],
    metadata: {},
    duplicateCount: 0,
    ...overrides,
  } as Alert;
}

describe("LinkSearchPicker", () => {
  it("shows no results until the query is non-empty", () => {
    render(
      <LinkSearchPicker candidates={[alertFixture()]} excludeIds={new Set()} onLink={vi.fn()} placeholder="Search..." />,
    );
    expect(screen.queryByText("Suspicious login")).not.toBeInTheDocument();
  });

  it("matches by title, id, asset, or srcIp", async () => {
    const alerts = [
      alertFixture({ id: "aaaaaaaa-0000-0000-0000-000000000000", title: "Suspicious login" }),
      alertFixture({ id: "bbbbbbbb-0000-0000-0000-000000000000", title: "Malware detected", asset: "web-01" }),
    ];
    render(<LinkSearchPicker candidates={alerts} excludeIds={new Set()} onLink={vi.fn()} placeholder="Search..." />);

    await userEvent.type(screen.getByPlaceholderText("Search..."), "web-01");
    expect(await screen.findByText(/Malware detected/)).toBeInTheDocument();
    expect(screen.queryByText("Suspicious login")).not.toBeInTheDocument();
  });

  it("excludes ids in excludeIds even on a matching query", async () => {
    const alerts = [alertFixture({ id: "aaaaaaaa-0000-0000-0000-000000000000", title: "Suspicious login" })];
    render(
      <LinkSearchPicker
        candidates={alerts}
        excludeIds={new Set(["aaaaaaaa-0000-0000-0000-000000000000"])}
        onLink={vi.fn()}
        placeholder="Search..."
      />,
    );

    await userEvent.type(screen.getByPlaceholderText("Search..."), "suspicious");
    expect(screen.queryByText("Suspicious login")).not.toBeInTheDocument();
  });

  it("calls onLink with the alert's id and clears the query on click", async () => {
    const onLink = vi.fn();
    const alerts = [alertFixture({ id: "aaaaaaaa-0000-0000-0000-000000000000", title: "Suspicious login" })];
    render(<LinkSearchPicker candidates={alerts} excludeIds={new Set()} onLink={onLink} placeholder="Search..." />);

    const input = screen.getByPlaceholderText("Search...");
    await userEvent.type(input, "suspicious");
    await userEvent.click(await screen.findByText(/Suspicious login/));

    expect(onLink).toHaveBeenCalledWith("aaaaaaaa-0000-0000-0000-000000000000");
    expect(input).toHaveValue("");
  });

  describe("keyboard navigation", () => {
    function threeAlerts() {
      return [
        alertFixture({ id: "aaaaaaaa-0000-0000-0000-000000000000", title: "First match" }),
        alertFixture({ id: "bbbbbbbb-0000-0000-0000-000000000000", title: "Second match" }),
        alertFixture({ id: "cccccccc-0000-0000-0000-000000000000", title: "Third match" }),
      ];
    }

    it("exposes combobox/listbox roles and activedescendant, matching CommandPalette's pattern", async () => {
      render(<LinkSearchPicker candidates={threeAlerts()} excludeIds={new Set()} onLink={vi.fn()} placeholder="Search..." />);
      const input = screen.getByPlaceholderText("Search...");
      await userEvent.type(input, "match");

      expect(input).toHaveAttribute("role", "combobox");
      expect(input).toHaveAttribute("aria-expanded", "true");
      expect(screen.getByRole("listbox")).toBeInTheDocument();
      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(3);
      // The first option is active by default (activeIndex starts at 0).
      expect(input).toHaveAttribute("aria-activedescendant", options[0].id);
      expect(options[0]).toHaveAttribute("aria-selected", "true");
    });

    it("ArrowDown/ArrowUp move the active option, wrapping at both ends", async () => {
      render(<LinkSearchPicker candidates={threeAlerts()} excludeIds={new Set()} onLink={vi.fn()} placeholder="Search..." />);
      const input = screen.getByPlaceholderText("Search...");
      await userEvent.type(input, "match");
      const options = screen.getAllByRole("option");

      await userEvent.keyboard("{ArrowDown}");
      expect(input).toHaveAttribute("aria-activedescendant", options[1].id);

      await userEvent.keyboard("{ArrowDown}");
      expect(input).toHaveAttribute("aria-activedescendant", options[2].id);

      // wraps back to the first option
      await userEvent.keyboard("{ArrowDown}");
      expect(input).toHaveAttribute("aria-activedescendant", options[0].id);

      // wraps to the last option going up from the first
      await userEvent.keyboard("{ArrowUp}");
      expect(input).toHaveAttribute("aria-activedescendant", options[2].id);
    });

    it("Enter links the active option without needing a mouse", async () => {
      const onLink = vi.fn();
      render(<LinkSearchPicker candidates={threeAlerts()} excludeIds={new Set()} onLink={onLink} placeholder="Search..." />);
      const input = screen.getByPlaceholderText("Search...");
      await userEvent.type(input, "match");

      await userEvent.keyboard("{ArrowDown}{Enter}");

      expect(onLink).toHaveBeenCalledWith("bbbbbbbb-0000-0000-0000-000000000000");
      expect(input).toHaveValue("");
    });

    it("Escape clears the query and closes the listbox", async () => {
      render(<LinkSearchPicker candidates={threeAlerts()} excludeIds={new Set()} onLink={vi.fn()} placeholder="Search..." />);
      const input = screen.getByPlaceholderText("Search...");
      await userEvent.type(input, "match");
      expect(screen.getByRole("listbox")).toBeInTheDocument();

      await userEvent.keyboard("{Escape}");

      expect(input).toHaveValue("");
      expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    });

    it("editing the query resets the active index back to the first option", async () => {
      render(<LinkSearchPicker candidates={threeAlerts()} excludeIds={new Set()} onLink={vi.fn()} placeholder="Search..." />);
      const input = screen.getByPlaceholderText("Search...");
      await userEvent.type(input, "match");
      await userEvent.keyboard("{ArrowDown}{ArrowDown}");
      const optionsBefore = screen.getAllByRole("option");
      expect(input).toHaveAttribute("aria-activedescendant", optionsBefore[2].id);

      // Re-type the same query (still 3 results) -- proves the reset comes
      // from onChange firing at all, not from the result set shrinking.
      await userEvent.clear(input);
      await userEvent.type(input, "match");
      const optionsAfter = screen.getAllByRole("option");
      expect(optionsAfter).toHaveLength(3);
      expect(input).toHaveAttribute("aria-activedescendant", optionsAfter[0].id);
    });
  });
});
