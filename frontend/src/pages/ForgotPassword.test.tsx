import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { ForgotPasswordPage } from "./ForgotPassword";

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/forgot-password"]}>
      <Routes>
        <Route path="/forgot-password" element={<ForgotPasswordPage />} />
        <Route path="/login" element={<div>Login Page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("ForgotPasswordPage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("submits the email and shows the generic confirmation message", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    renderPage();

    await userEvent.type(screen.getByLabelText("E-mail"), "someone@test.local");
    await userEvent.click(screen.getByRole("button", { name: "Send reset link" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/auth/password-reset/request",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    expect(await screen.findByText(/reset link is on its way/)).toBeInTheDocument();
  });

  it("shows the same confirmation message even for an email the backend won't recognize", async () => {
    // the endpoint always returns 204 -- this just re-confirms the page
    // doesn't try to special-case anything client-side either.
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    renderPage();

    await userEvent.type(screen.getByLabelText("E-mail"), "nobody@test.local");
    await userEvent.click(screen.getByRole("button", { name: "Send reset link" }));

    expect(await screen.findByText(/reset link is on its way/)).toBeInTheDocument();
  });

  it("shows an error banner on an unexpected server failure", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(500, { error: "internal error" })));
    renderPage();

    await userEvent.type(screen.getByLabelText("E-mail"), "someone@test.local");
    await userEvent.click(screen.getByRole("button", { name: "Send reset link" }));

    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });

  it("links back to the login page", async () => {
    vi.stubGlobal("fetch", vi.fn());
    renderPage();

    await userEvent.click(screen.getByRole("link", { name: "Back to sign in" }));
    expect(await screen.findByText("Login Page")).toBeInTheDocument();
  });
});
