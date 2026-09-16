import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ChangePasswordPage } from "./ChangePassword";
import { AuthProvider } from "../auth/AuthContext";
import { seedSession, withSession } from "../test/session";

function renderWithSession() {
  seedSession({ id: "1", email: "admin@kuruops.local", name: "Admin", role: "admin", mustChangePassword: true, resourceAccess: [] })
  return render(
    <AuthProvider>
      <ChangePasswordPage />
    </AuthProvider>,
  );
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("ChangePasswordPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("rejects mismatched passwords without calling the API", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn()));
    renderWithSession();

    await userEvent.type(screen.getByLabelText("Current password"), "ChangeMe123!");
    await userEvent.type(screen.getByLabelText("New password"), "NewPassword123!");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "Different123!");
    await userEvent.click(screen.getByRole("button", { name: "Save new password" }));

    expect(await screen.findByText("The new passwords don't match.")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("rejects a new password shorter than 8 characters without calling the API", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn()));
    renderWithSession();

    await userEvent.type(screen.getByLabelText("Current password"), "ChangeMe123!");
    await userEvent.type(screen.getByLabelText("New password"), "short");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "short");
    await userEvent.click(screen.getByRole("button", { name: "Save new password" }));

    expect(await screen.findByText("The new password must be at least 8 characters.")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("rejects a new password with only letters, without calling the API", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn()));
    renderWithSession();

    await userEvent.type(screen.getByLabelText("Current password"), "ChangeMe123!");
    await userEvent.type(screen.getByLabelText("New password"), "onlyletters");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "onlyletters");
    await userEvent.click(screen.getByRole("button", { name: "Save new password" }));

    expect(await screen.findByText("The new password must contain at least one letter and one digit.")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("rejects a new password with only digits, without calling the API", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn()));
    renderWithSession();

    await userEvent.type(screen.getByLabelText("Current password"), "ChangeMe123!");
    await userEvent.type(screen.getByLabelText("New password"), "12345678");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "12345678");
    await userEvent.click(screen.getByRole("button", { name: "Save new password" }));

    expect(await screen.findByText("The new password must contain at least one letter and one digit.")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("submits the change and clears the error on success", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse(200, { token: "new.token.here" }))));
    renderWithSession();

    await userEvent.type(screen.getByLabelText("Current password"), "ChangeMe123!");
    await userEvent.type(screen.getByLabelText("New password"), "NewPassword123!");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "NewPassword123!");
    await userEvent.click(screen.getByRole("button", { name: "Save new password" }));

    await waitFor(() => expect(fetch).toHaveBeenCalledWith(
      "/api/v1/account/change-password",
      expect.objectContaining({ method: "POST" }),
    ));
  });

  it("shows the server's error message on failure", async () => {
    vi.stubGlobal("fetch", withSession(vi.fn().mockResolvedValue(jsonResponse(400, { error: "current password is incorrect" }))));
    renderWithSession();

    await userEvent.type(screen.getByLabelText("Current password"), "wrong");
    await userEvent.type(screen.getByLabelText("New password"), "NewPassword123!");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "NewPassword123!");
    await userEvent.click(screen.getByRole("button", { name: "Save new password" }));

    expect(await screen.findByText("current password is incorrect")).toBeInTheDocument();
  });
});
