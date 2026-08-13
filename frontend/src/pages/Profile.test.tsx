import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { ProfilePage } from "./Profile";
import { AuthProvider } from "../auth/AuthContext";

function renderWithSession() {
  localStorage.setItem(
    "argusops.session",
    JSON.stringify({
      token: "tok",
      refreshToken: "rt",
      user: { id: "1", email: "analyst@argusops.local", name: "Ana Lyst", role: "analyst", mustChangePassword: false, resourceAccess: [] },
    }),
  );
  return render(
    <MemoryRouter>
      <AuthProvider>
        <ProfilePage />
      </AuthProvider>
    </MemoryRouter>,
  );
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("ProfilePage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("loads the current name/email into the form", () => {
    vi.stubGlobal("fetch", vi.fn());
    renderWithSession();

    expect(screen.getByLabelText("Name")).toHaveValue("Ana Lyst");
    expect(screen.getByLabelText("Email")).toHaveValue("analyst@argusops.local");
  });

  it("does not show the profile's current-password field until the email is changed", async () => {
    vi.stubGlobal("fetch", vi.fn());
    renderWithSession();

    // The password-change section below always has its own "Current
    // password" field -- only the profile form's conditional one (with the
    // "(required to change your email)" hint) is being asserted here.
    expect(screen.queryByLabelText(/required to change your email/)).not.toBeInTheDocument();

    await userEvent.clear(screen.getByLabelText("Email"));
    await userEvent.type(screen.getByLabelText("Email"), "new@argusops.local");

    expect(screen.getByLabelText(/required to change your email/)).toBeInTheDocument();
  });

  it("a name-only change PUTs the profile without requiring a password", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api-tokens")) return Promise.resolve(jsonResponse(200, []));
      return Promise.resolve(jsonResponse(200, {}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderWithSession();

    await userEvent.clear(screen.getByLabelText("Name"));
    await userEvent.type(screen.getByLabelText("Name"), "New Name");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/account/profile", expect.objectContaining({ method: "PUT" })),
    );
    expect(await screen.findByText("Saved.")).toBeInTheDocument();
  });

  it("shows the server's error message when the profile update fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/api-tokens")) return Promise.resolve(jsonResponse(200, []));
        return Promise.resolve(jsonResponse(400, { error: "current password is incorrect" }));
      }),
    );
    renderWithSession();

    await userEvent.clear(screen.getByLabelText("Email"));
    await userEvent.type(screen.getByLabelText("Email"), "new@argusops.local");
    await userEvent.type(screen.getByLabelText(/required to change your email/), "wrong");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("current password is incorrect")).toBeInTheDocument();
  });

  it("password section rejects mismatched passwords without calling the API", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    renderWithSession();

    await userEvent.type(screen.getByLabelText("Current password"), "ChangeMe123!");
    await userEvent.type(screen.getByLabelText("New password"), "NewPassword123!");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "Different123!");
    await userEvent.click(screen.getByRole("button", { name: "Save new password" }));

    expect(await screen.findByText("The new passwords don't match.")).toBeInTheDocument();
    // Not a blanket "fetch was never called" -- the API Tokens section
    // fetches its own list on mount, unrelated to this form. Only the
    // change-password endpoint itself must stay untouched.
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/account/change-password", expect.anything());
  });

  it("password section submits the change to the change-password endpoint", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api-tokens")) return Promise.resolve(jsonResponse(200, []));
      return Promise.resolve(jsonResponse(200, { token: "new.token.here" }));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderWithSession();

    await userEvent.type(screen.getByLabelText("Current password"), "ChangeMe123!");
    await userEvent.type(screen.getByLabelText("New password"), "NewPassword123!");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "NewPassword123!");
    await userEvent.click(screen.getByRole("button", { name: "Save new password" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/account/change-password", expect.objectContaining({ method: "POST" })),
    );
  });

  it("API Tokens: shows an empty state when there are none yet", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/api-tokens")) return Promise.resolve(jsonResponse(200, []));
        return Promise.resolve(jsonResponse(200, {}));
      }),
    );
    renderWithSession();

    expect(await screen.findByText("No API tokens yet.")).toBeInTheDocument();
  });

  it("API Tokens: creating one shows the plaintext once, then lists it", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/api-tokens") && init?.method === "POST") {
        return Promise.resolve(
          jsonResponse(201, {
            token: { id: "t1", tenantId: "tn1", userId: "1", name: "CI script", tokenLast4: "abcd", createdAt: "2026-01-01T00:00:00Z" },
            plaintext: "pat_abcdefghijklmnopqrstuvwxabcd",
          }),
        );
      }
      if (url.includes("/api-tokens")) return Promise.resolve(jsonResponse(200, []));
      return Promise.resolve(jsonResponse(200, {}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderWithSession();

    await screen.findByText("No API tokens yet.");
    await userEvent.click(screen.getByRole("button", { name: "+ New Token" }));
    await userEvent.type(screen.getByLabelText("Token Name"), "CI script");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("pat_abcdefghijklmnopqrstuvwxabcd")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/account/api-tokens",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ name: "CI script", expiresInDays: 90 }) }),
    );
  });

  it("API Tokens: revoke calls DELETE and removes it from the active list", async () => {
    let revoked = false;
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/api-tokens/t1") && init?.method === "DELETE") {
        revoked = true;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (url.includes("/api-tokens")) {
        const tok = { id: "t1", tenantId: "tn1", userId: "1", name: "CI script", tokenLast4: "abcd", createdAt: "2026-01-01T00:00:00Z" };
        return Promise.resolve(jsonResponse(200, revoked ? [{ ...tok, revokedAt: "2026-01-02T00:00:00Z" }] : [tok]));
      }
      return Promise.resolve(jsonResponse(200, {}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderWithSession();

    expect(await screen.findByText("CI script")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/account/api-tokens/t1", expect.objectContaining({ method: "DELETE" })),
    );
    expect(await screen.findByText("No API tokens yet.")).toBeInTheDocument();
  });
});
