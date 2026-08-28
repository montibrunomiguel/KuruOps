import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { ProfilePage } from "./Profile";
import { AuthProvider } from "../auth/AuthContext";

function renderWithSession(phone?: string, mfaEnabled = false) {
  localStorage.setItem(
    "kuruops.session",
    JSON.stringify({
      token: "tok",
      refreshToken: "rt",
      user: {
        id: "1",
        email: "analyst@kuruops.local",
        name: "Ana Lyst",
        phone,
        role: "analyst",
        mustChangePassword: false,
        resourceAccess: [],
        mfaEnabled,
      },
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

// A fake JWT whose payload segment decodes to the given claims -- same
// shape AuthContext.test.tsx's fakeToken uses, needed here since
// ConfirmMFA/DisableMFA's responses are re-decoded via applyNewToken.
function fakeToken(claims: Record<string, unknown>): string {
  const header = btoa(JSON.stringify({ alg: "RS256" }));
  const payload = btoa(JSON.stringify(claims)).replace(/\+/g, "-").replace(/\//g, "_");
  return `${header}.${payload}.fake-signature`;
}

describe("ProfilePage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("loads the current name/email/phone into the form", () => {
    vi.stubGlobal("fetch", vi.fn());
    renderWithSession("+5511912345678");

    expect(screen.getByLabelText("Name")).toHaveValue("Ana Lyst");
    expect(screen.getByLabelText("Email")).toHaveValue("analyst@kuruops.local");
    expect(screen.getByLabelText(/Phone/)).toHaveValue("+5511912345678");
  });

  it("a phone change is included in the profile PUT body", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api-tokens")) return Promise.resolve(jsonResponse(200, []));
      return Promise.resolve(jsonResponse(200, {}));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderWithSession();

    await userEvent.type(screen.getByLabelText(/Phone/), "+5511912345678");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/account/profile",
        expect.objectContaining({ method: "PUT", body: expect.stringContaining("+5511912345678") }),
      ),
    );
  });

  it("shows the server's error message when an invalid phone is rejected", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/api-tokens")) return Promise.resolve(jsonResponse(200, []));
        return Promise.resolve(jsonResponse(400, { error: "phone must include a country code, e.g. +5511912345678" }));
      }),
    );
    renderWithSession();

    await userEvent.type(screen.getByLabelText(/Phone/), "5511912345678");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText(/phone must include a country code/)).toBeInTheDocument();
  });

  it("does not show the profile's current-password field until the email is changed", async () => {
    vi.stubGlobal("fetch", vi.fn());
    renderWithSession();

    // The password-change section below always has its own "Current
    // password" field -- only the profile form's conditional one (with the
    // "(required to change your email)" hint) is being asserted here.
    expect(screen.queryByLabelText(/required to change your email/)).not.toBeInTheDocument();

    await userEvent.clear(screen.getByLabelText("Email"));
    await userEvent.type(screen.getByLabelText("Email"), "new@kuruops.local");

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
    await userEvent.type(screen.getByLabelText("Email"), "new@kuruops.local");
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

  describe("Two-Factor Authentication", () => {
    it("shows an Enable button and no Enabled badge when MFA is off", async () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(200, [])));
      renderWithSession(undefined, false);

      expect(await screen.findByRole("button", { name: "Enable" })).toBeInTheDocument();
      expect(screen.queryByText("Enabled")).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Disable" })).not.toBeInTheDocument();
    });

    it("Enable fetches a secret + QR and shows the confirm-code step", async () => {
      const fetchMock = vi.fn().mockImplementation((url: string) => {
        if (url.includes("/mfa/enroll")) {
          return Promise.resolve(
            jsonResponse(200, { secret: "JBSWY3DPEHPK3PXP", otpauthUrl: "otpauth://totp/KuruOps:analyst@kuruops.local?secret=JBSWY3DPEHPK3PXP&issuer=KuruOps" }),
          );
        }
        return Promise.resolve(jsonResponse(200, []));
      });
      vi.stubGlobal("fetch", fetchMock);
      renderWithSession(undefined, false);

      await userEvent.click(await screen.findByRole("button", { name: "Enable" }));

      expect(await screen.findByText("JBSWY3DPEHPK3PXP")).toBeInTheDocument();
      expect(screen.getByLabelText("Code")).toBeInTheDocument();
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/account/mfa/enroll", expect.objectContaining({ method: "POST" }));
    });

    it("Cancel during enrollment returns to the Enable button without confirming", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockImplementation((url: string) => {
          if (url.includes("/mfa/enroll")) return Promise.resolve(jsonResponse(200, { secret: "SECRET123", otpauthUrl: "otpauth://totp/x" }));
          return Promise.resolve(jsonResponse(200, []));
        }),
      );
      renderWithSession(undefined, false);

      await userEvent.click(await screen.findByRole("button", { name: "Enable" }));
      await screen.findByText("SECRET123");
      await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

      expect(screen.getByRole("button", { name: "Enable" })).toBeInTheDocument();
      expect(screen.queryByText("SECRET123")).not.toBeInTheDocument();
    });

    it("confirming with the code PUTs the secret+code and activates it", async () => {
      const freshToken = fakeToken({ mfa_enabled: true });
      const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
        if (url.includes("/mfa/enroll")) return Promise.resolve(jsonResponse(200, { secret: "SECRET123", otpauthUrl: "otpauth://totp/x" }));
        if (url.endsWith("/api/v1/account/mfa") && init?.method === "PUT") {
          return Promise.resolve(jsonResponse(200, { token: freshToken }));
        }
        return Promise.resolve(jsonResponse(200, []));
      });
      vi.stubGlobal("fetch", fetchMock);
      renderWithSession(undefined, false);

      await userEvent.click(await screen.findByRole("button", { name: "Enable" }));
      await screen.findByText("SECRET123");
      await userEvent.type(screen.getByLabelText("Code"), "123456");
      await userEvent.click(screen.getByRole("button", { name: "Activate" }));

      await waitFor(() =>
        expect(fetchMock).toHaveBeenCalledWith(
          "/api/v1/account/mfa",
          expect.objectContaining({ method: "PUT", body: JSON.stringify({ secret: "SECRET123", code: "123456" }) }),
        ),
      );
      expect(await screen.findByText("Enabled")).toBeInTheDocument();
      expect(await screen.findByRole("button", { name: "Disable" })).toBeInTheDocument();
    });

    it("shows the server's error when confirming with a wrong code", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockImplementation((url: string, init?: RequestInit) => {
          if (url.includes("/mfa/enroll")) return Promise.resolve(jsonResponse(200, { secret: "SECRET123", otpauthUrl: "otpauth://totp/x" }));
          if (url.endsWith("/api/v1/account/mfa") && init?.method === "PUT") return Promise.resolve(jsonResponse(400, { error: "invalid code" }));
          return Promise.resolve(jsonResponse(200, []));
        }),
      );
      renderWithSession(undefined, false);

      await userEvent.click(await screen.findByRole("button", { name: "Enable" }));
      await screen.findByText("SECRET123");
      await userEvent.type(screen.getByLabelText("Code"), "000000");
      await userEvent.click(screen.getByRole("button", { name: "Activate" }));

      expect(await screen.findByText("invalid code")).toBeInTheDocument();
    });

    it("when MFA is on, shows the Enabled badge and a Disable button", async () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(200, [])));
      renderWithSession(undefined, true);

      expect(await screen.findByText("Enabled")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Disable" })).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Enable" })).not.toBeInTheDocument();
    });

    it("Disable reveals a password field, and a wrong password shows an error without disabling", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockImplementation((url: string, init?: RequestInit) => {
          if (url.endsWith("/api/v1/account/mfa") && init?.method === "DELETE") return Promise.resolve(jsonResponse(400, { error: "current password is incorrect" }));
          return Promise.resolve(jsonResponse(200, []));
        }),
      );
      renderWithSession(undefined, true);

      await userEvent.click(await screen.findByRole("button", { name: "Disable" }));
      const pwField = screen.getByLabelText(/required to turn off two-factor authentication/);
      await userEvent.type(pwField, "wrong");
      await userEvent.click(screen.getByRole("button", { name: "Confirm & Disable" }));

      expect(await screen.findByText("current password is incorrect")).toBeInTheDocument();
      expect(screen.getByText("Enabled")).toBeInTheDocument();
    });

    it("Disable with the correct password turns MFA off", async () => {
      const freshToken = fakeToken({ mfa_enabled: false });
      const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
        if (url.endsWith("/api/v1/account/mfa") && init?.method === "DELETE") {
          return Promise.resolve(jsonResponse(200, { token: freshToken }));
        }
        return Promise.resolve(jsonResponse(200, []));
      });
      vi.stubGlobal("fetch", fetchMock);
      renderWithSession(undefined, true);

      await userEvent.click(await screen.findByRole("button", { name: "Disable" }));
      await userEvent.type(screen.getByLabelText(/required to turn off two-factor authentication/), "ChangeMe123!");
      await userEvent.click(screen.getByRole("button", { name: "Confirm & Disable" }));

      await waitFor(() =>
        expect(fetchMock).toHaveBeenCalledWith(
          "/api/v1/account/mfa",
          expect.objectContaining({ method: "DELETE", body: JSON.stringify({ currentPassword: "ChangeMe123!" }) }),
        ),
      );
      expect(await screen.findByRole("button", { name: "Enable" })).toBeInTheDocument();
      expect(screen.queryByText("Enabled")).not.toBeInTheDocument();
    });
  });
});
