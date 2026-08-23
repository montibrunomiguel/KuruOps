import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { ResetPasswordPage } from "./ResetPassword";

function renderPage(path = "/reset-password?token=abc123") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/reset-password" element={<ResetPasswordPage />} />
        <Route path="/login" element={<div>Login Page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("ResetPasswordPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows a missing-token message and no form when the link has no token", () => {
    vi.stubGlobal("fetch", vi.fn());
    renderPage("/reset-password");

    expect(screen.getByText(/missing its token/)).toBeInTheDocument();
    expect(screen.queryByLabelText("New password")).not.toBeInTheDocument();
  });

  it("rejects mismatched passwords without calling the API", async () => {
    vi.stubGlobal("fetch", vi.fn());
    renderPage();

    await userEvent.type(screen.getByLabelText("New password"), "NewPassword123!");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "Different123!");
    await userEvent.click(screen.getByRole("button", { name: "Reset password" }));

    expect(await screen.findByText("The new passwords don't match.")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("rejects a password shorter than 8 characters without calling the API", async () => {
    vi.stubGlobal("fetch", vi.fn());
    renderPage();

    await userEvent.type(screen.getByLabelText("New password"), "short");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "short");
    await userEvent.click(screen.getByRole("button", { name: "Reset password" }));

    expect(await screen.findByText("The new password must be at least 8 characters.")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("rejects a password with only digits without calling the API", async () => {
    vi.stubGlobal("fetch", vi.fn());
    renderPage();

    await userEvent.type(screen.getByLabelText("New password"), "12345678");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "12345678");
    await userEvent.click(screen.getByRole("button", { name: "Reset password" }));

    expect(await screen.findByText("The new password must contain at least one letter and one digit.")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("submits the token and new password, then shows the done message", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    renderPage();

    await userEvent.type(screen.getByLabelText("New password"), "NewPassword123!");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "NewPassword123!");
    await userEvent.click(screen.getByRole("button", { name: "Reset password" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/auth/password-reset/confirm",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    expect(await screen.findByText(/password has been reset/)).toBeInTheDocument();
  });

  it("shows the server's error for an invalid or expired token", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(400, { error: "invalid or expired reset link" })));
    renderPage();

    await userEvent.type(screen.getByLabelText("New password"), "NewPassword123!");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "NewPassword123!");
    await userEvent.click(screen.getByRole("button", { name: "Reset password" }));

    expect(await screen.findByText("invalid or expired reset link")).toBeInTheDocument();
  });

  it("links back to the login page", async () => {
    vi.stubGlobal("fetch", vi.fn());
    renderPage();

    await userEvent.click(screen.getByRole("link", { name: "Back to sign in" }));
    expect(await screen.findByText("Login Page")).toBeInTheDocument();
  });
});
