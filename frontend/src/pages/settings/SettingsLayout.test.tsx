import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { SettingsLayout } from "./SettingsLayout";
import { AuthProvider } from "../../auth/AuthContext";

// A path that matches none of SettingsLayout's own internal <Route>s --
// this suite is about the nav search filter, not routing/panel content, so
// no panel mounts and there's nothing to mock fetch for.
function renderLayout() {
  return render(
    <MemoryRouter initialEntries={["/settings/__none__"]}>
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
