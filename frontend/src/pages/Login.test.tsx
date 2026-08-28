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
        jsonResponse(200, { token, user: { id: "1", email: "admin@kuruops.local", name: "Admin", role: "admin", mustChangePassword: false } }),
      ),
    );

    renderLogin();
    await userEvent.type(screen.getByLabelText("E-mail"), "admin@kuruops.local");
    await userEvent.type(screen.getByLabelText("Password"), "ChangeMe123!");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));

    await waitFor(() => expect(screen.getByText("Home Page")).toBeInTheDocument());
  });

  it("shows an error banner on invalid credentials, without navigating", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(401, { error: "invalid credentials" })));

    renderLogin();
    await userEvent.type(screen.getByLabelText("E-mail"), "admin@kuruops.local");
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

  describe("two-factor step", () => {
    it("a correct password for a TOTP-enrolled account switches to the code step, not straight to Home", async () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(200, { mfaRequired: true, pendingToken: "mfap_abc123" })));

      renderLogin();
      await userEvent.type(screen.getByLabelText("E-mail"), "admin@kuruops.local");
      await userEvent.type(screen.getByLabelText("Password"), "ChangeMe123!");
      await userEvent.click(screen.getByRole("button", { name: "Sign in" }));

      expect(await screen.findByText("Two-Factor Authentication")).toBeInTheDocument();
      expect(screen.queryByText("Home Page")).not.toBeInTheDocument();
    });

    it("submitting a correct code navigates to /", async () => {
      const token = `${btoa(JSON.stringify({}))}.${btoa(JSON.stringify({ resource_access: [] }))}.sig`;
      const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
        const body = init?.body ? JSON.parse(init.body as string) : {};
        if ("pendingToken" in body) {
          return Promise.resolve(
            jsonResponse(200, { token, refreshToken: "rt_1", user: { id: "1", email: "admin@kuruops.local", name: "Admin", role: "admin", mustChangePassword: false } }),
          );
        }
        return Promise.resolve(jsonResponse(200, { mfaRequired: true, pendingToken: "mfap_abc123" }));
      });
      vi.stubGlobal("fetch", fetchMock);

      renderLogin();
      await userEvent.type(screen.getByLabelText("E-mail"), "admin@kuruops.local");
      await userEvent.type(screen.getByLabelText("Password"), "ChangeMe123!");
      await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
      await screen.findByText("Two-Factor Authentication");

      await userEvent.type(screen.getByLabelText("Code"), "123456");
      await userEvent.click(screen.getByRole("button", { name: "Verify" }));

      await waitFor(() => expect(screen.getByText("Home Page")).toBeInTheDocument());
      const call = fetchMock.mock.calls.find((c) => c[0] === "/auth/mfa/verify");
      expect(call).toBeTruthy();
      expect(JSON.parse((call![1] as RequestInit).body as string)).toEqual({ pendingToken: "mfap_abc123", code: "123456" });
    });

    it("a wrong code shows an error banner and stays on the code step", async () => {
      const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
        const body = init?.body ? JSON.parse(init.body as string) : {};
        if ("pendingToken" in body) return Promise.resolve(jsonResponse(401, { error: "invalid or expired code" }));
        return Promise.resolve(jsonResponse(200, { mfaRequired: true, pendingToken: "mfap_abc123" }));
      });
      vi.stubGlobal("fetch", fetchMock);

      renderLogin();
      await userEvent.type(screen.getByLabelText("E-mail"), "admin@kuruops.local");
      await userEvent.type(screen.getByLabelText("Password"), "ChangeMe123!");
      await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
      await screen.findByText("Two-Factor Authentication");

      await userEvent.type(screen.getByLabelText("Code"), "000000");
      await userEvent.click(screen.getByRole("button", { name: "Verify" }));

      expect(await screen.findByText("invalid or expired code")).toBeInTheDocument();
      expect(screen.queryByText("Home Page")).not.toBeInTheDocument();
    });

    it("Back returns to the email/password step without submitting a code", async () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(200, { mfaRequired: true, pendingToken: "mfap_abc123" })));

      renderLogin();
      await userEvent.type(screen.getByLabelText("E-mail"), "admin@kuruops.local");
      await userEvent.type(screen.getByLabelText("Password"), "ChangeMe123!");
      await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
      await screen.findByText("Two-Factor Authentication");

      await userEvent.click(screen.getByRole("button", { name: "Back" }));

      expect(screen.getByLabelText("E-mail")).toBeInTheDocument();
      expect(screen.queryByText("Two-Factor Authentication")).not.toBeInTheDocument();
    });
  });
});
