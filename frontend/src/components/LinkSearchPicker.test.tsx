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
});
