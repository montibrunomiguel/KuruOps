import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { SettingsLayout } from "./SettingsLayout";
import { AuthProvider } from "../../auth/AuthContext";

// A path that matches none of SettingsLayout's own internal <Route>s --
// this suite is about the nav search filter, not routing/panel content, so
// no panel mounts and there's nothing to mock fetch for.
function renderLayout(initialPath = "/settings/__none__") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <AuthProvider>
        <SettingsLayout />
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("SettingsLayout nav search", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows every nav item when the search box is empty", () => {
    renderLayout();
    expect(screen.getByRole("link", { name: "Webhook Endpoints" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Retention" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Slack" })).toBeInTheDocument();
  });

  it("filters nav items to those matching the query, case-insensitively", async () => {
    renderLayout();
    await userEvent.type(screen.getByPlaceholderText("Search settings..."), "webhook");

    expect(screen.getByRole("link", { name: "Webhook Endpoints" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Retention" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Slack" })).not.toBeInTheDocument();
  });

  it("hides a group entirely once none of its items match", async () => {
    renderLayout();
    await userEvent.type(screen.getByPlaceholderText("Search settings..."), "webhook");

    expect(screen.getByText("Integrations")).toBeInTheDocument();
    expect(screen.queryByText("Connectors")).not.toBeInTheDocument();
    expect(screen.queryByText("Data & Audit")).not.toBeInTheDocument();
  });

  it("shows a no-results message when nothing matches", async () => {
    renderLayout();
    await userEvent.type(screen.getByPlaceholderText("Search settings..."), "xyzxyz-nomatch");

    expect(screen.getByText("No matching settings.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Webhook Endpoints" })).not.toBeInTheDocument();
  });

  it("restores every item once the query is cleared", async () => {
    renderLayout();
    const search = screen.getByPlaceholderText("Search settings...");
    await userEvent.type(search, "webhook");
    expect(screen.queryByRole("link", { name: "Retention" })).not.toBeInTheDocument();

    await userEvent.clear(search);
    expect(screen.getByRole("link", { name: "Retention" })).toBeInTheDocument();
  });
});

describe("SettingsLayout nav collapse/expand", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("collapses a group's items on toggle click and expands them again on a second click", async () => {
    renderLayout();
    const toggle = screen.getByRole("button", { name: "Integrations" });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "Webhook Endpoints" })).toBeInTheDocument();

    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "Webhook Endpoints" })).not.toBeInTheDocument();

    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "Webhook Endpoints" })).toBeInTheDocument();
  });

  it("persists collapsed state across a remount via localStorage", async () => {
    const { unmount } = renderLayout();
    await userEvent.click(screen.getByRole("button", { name: "Integrations" }));
    expect(screen.queryByRole("link", { name: "Webhook Endpoints" })).not.toBeInTheDocument();
    unmount();

    renderLayout();
    expect(screen.getByRole("button", { name: "Integrations" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "Webhook Endpoints" })).not.toBeInTheDocument();
  });

  it("keeps the group containing the active route expanded even when stored as collapsed", async () => {
    const { unmount } = renderLayout();
    await userEvent.click(screen.getByRole("button", { name: "Integrations" }));
    unmount();

    renderLayout("/settings/webhooks");
    expect(screen.getByRole("button", { name: "Integrations" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "Webhook Endpoints" })).toBeInTheDocument();
  });

  it("still surfaces a matching item from a collapsed group while searching", async () => {
    const { unmount } = renderLayout();
    await userEvent.click(screen.getByRole("button", { name: "Integrations" }));
    unmount();

    renderLayout();
    await userEvent.type(screen.getByPlaceholderText("Search settings..."), "webhook");
    expect(screen.getByRole("link", { name: "Webhook Endpoints" })).toBeInTheDocument();
  });
});
