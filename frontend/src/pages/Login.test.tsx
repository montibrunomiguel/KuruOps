import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { LoginPage } from "./Login";
import { AuthProvider } from "../auth/AuthContext";

function renderLogin() {
  return render(
    <MemoryRouter initialEntries={["/login"]}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<div>Home Page</div>} />
          <Route path="/forgot-password" element={<div>Forgot Password Page</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("LoginPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("submits email/password and navigates to / on success", async () => {
    const token = `${btoa(JSON.stringify({}))}.${btoa(JSON.stringify({ resource_access: ["alerts"] }))}.sig`;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(200, { token, user: { id: "1", email: "admin@argusops.local", name: "Admin", role: "admin", mustChangePassword: false } }),
      ),
    );

    renderLogin();
    await userEvent.type(screen.getByLabelText("E-mail"), "admin@argusops.local");
    await userEvent.type(screen.getByLabelText("Password"), "ChangeMe123!");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));

    await waitFor(() => expect(screen.getByText("Home Page")).toBeInTheDocument());
  });

  it("shows an error banner on invalid credentials, without navigating", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(401, { error: "invalid credentials" })));

    renderLogin();
    await userEvent.type(screen.getByLabelText("E-mail"), "admin@argusops.local");
    await userEvent.type(screen.getByLabelText("Password"), "wrong");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("invalid credentials")).toBeInTheDocument();
    expect(screen.queryByText("Home Page")).not.toBeInTheDocument();
  });

  it("disables the submit button while submitting", async () => {
    let resolveFetch!: (value: Response) => void;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockReturnValue(new Promise<Response>((resolve) => (resolveFetch = resolve))),
    );

    renderLogin();
    await userEvent.type(screen.getByLabelText("E-mail"), "a@b.com");
    await userEvent.type(screen.getByLabelText("Password"), "pw");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));

    expect(screen.getByRole("button", { name: "Signing in..." })).toBeDisabled();
    resolveFetch(jsonResponse(200, { token: "a.b.c", user: { id: "1", email: "a@b.com", name: "A", role: "viewer", mustChangePassword: false } }));
  });

  it("links to the forgot-password page", async () => {
    vi.stubGlobal("fetch", vi.fn());
    renderLogin();

    await userEvent.click(screen.getByRole("link", { name: "Forgot password?" }));
    expect(await screen.findByText("Forgot Password Page")).toBeInTheDocument();
  });
});
