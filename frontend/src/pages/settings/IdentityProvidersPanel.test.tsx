import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { IdentityProvidersPanel } from "./IdentityProvidersPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <IdentityProvidersPanel />
    </AuthProvider>,
  );
}

describe("IdentityProvidersPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows 'Configure' when neither LDAP nor SAML is set up yet", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    expect(await screen.findByRole("button", { name: "Configure LDAP" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Configure SAML" })).toBeInTheDocument();
    expect(screen.queryByText("configured")).not.toBeInTheDocument();
  });

  it("LDAP bind password is required only when no config exists yet", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    await screen.findByRole("button", { name: "Configure LDAP" });
    expect(screen.getByLabelText(/Bind password/)).toBeRequired();
  });

  it("shows 'Update' and a badge once LDAP/SAML are configured, bind password no longer required", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/ldap")) {
          return Promise.resolve(
            jsonResponse({
              host: "ldap.example.com", port: 636, useTls: true, bindDn: "cn=svc",
              userBaseDn: "ou=people", userFilter: "(mail=%s)", groupBaseDn: "", groupAttribute: "memberOf",
            }),
          );
        }
        return Promise.resolve(
          jsonResponse({
            spEntityId: "https://argusops.example/saml", acsUrl: "https://argusops.example/acs",
            idpMetadataUrl: "https://idp.example.com/metadata",
          }),
        );
      }),
    );
    renderPanel();

    expect(await screen.findAllByRole("button", { name: "Update" })).toHaveLength(2);
    expect(screen.getAllByText("configured")).toHaveLength(2);
    expect(screen.getByLabelText(/Bind password/)).not.toBeRequired();
  });

  it("saving the LDAP form PUTs the config and shows a success message", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(null));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.type(await screen.findByLabelText("Host"), "ldap.example.com");
    await userEvent.type(screen.getByLabelText("Bind DN (service account)"), "cn=svc,dc=example,dc=com");
    await userEvent.type(screen.getByLabelText(/Bind password/), "s3cret");
    await userEvent.type(screen.getByLabelText("User base DN"), "ou=people,dc=example,dc=com");
    await userEvent.click(screen.getByRole("button", { name: "Configure LDAP" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/identity-providers/ldap", expect.objectContaining({ method: "PUT" })),
    );
    expect(await screen.findByText("Configuration saved.")).toBeInTheDocument();
  });

  it("switching SAML metadata mode toggles between URL and XML inputs", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    await screen.findByLabelText("IdP Metadata URL");
    expect(screen.queryByLabelText("IdP Metadata XML")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Pasted XML" }));
    expect(screen.getByLabelText("IdP Metadata XML")).toBeInTheDocument();
    expect(screen.queryByLabelText("IdP Metadata URL")).not.toBeInTheDocument();
  });

  it("shows a Remove button once configured, and removing clears the form back to Configure", async () => {
    let ldapRemoved = false;
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") {
        ldapRemoved = true;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (url.includes("/ldap")) {
        return Promise.resolve(
          jsonResponse(
            ldapRemoved
              ? null
              : {
                  host: "ldap.example.com", port: 636, useTls: true, bindDn: "cn=svc",
                  userBaseDn: "ou=people", userFilter: "(mail=%s)", groupBaseDn: "", groupAttribute: "memberOf",
                },
          ),
        );
      }
      return Promise.resolve(
        jsonResponse({
          spEntityId: "https://argusops.example/saml", acsUrl: "https://argusops.example/acs",
          idpMetadataUrl: "https://idp.example.com/metadata",
        }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    expect(await screen.findAllByRole("button", { name: "Remove configuration" })).toHaveLength(2);

    const [ldapRemove] = screen.getAllByRole("button", { name: "Remove configuration" });
    await userEvent.click(ldapRemove);
    await userEvent.click(await screen.findByRole("button", { name: "Confirm delete" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/identity-providers/ldap", expect.objectContaining({ method: "DELETE" })),
    );
    expect(await screen.findByRole("button", { name: "Configure LDAP" })).toBeInTheDocument();
  });

  it("cancelling the removal confirmation leaves the config untouched", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") throw new Error("must not be called when confirm is cancelled");
      if (url.includes("/ldap")) {
        return Promise.resolve(
          jsonResponse({
            host: "ldap.example.com", port: 636, useTls: true, bindDn: "cn=svc",
            userBaseDn: "ou=people", userFilter: "(mail=%s)", groupBaseDn: "", groupAttribute: "memberOf",
          }),
        );
      }
      return Promise.resolve(
        jsonResponse({
          spEntityId: "https://argusops.example/saml", acsUrl: "https://argusops.example/acs",
          idpMetadataUrl: "https://idp.example.com/metadata",
        }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    const [ldapRemove] = await screen.findAllByRole("button", { name: "Remove configuration" });
    await userEvent.click(ldapRemove);
    await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));

    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/identity-providers/ldap", expect.objectContaining({ method: "DELETE" }));
    expect(screen.getAllByText("configured")).toHaveLength(2);
  });

  it("a fetch error surfaces the error banner instead of the form silently failing", async () => {
    // mockImplementation (not mockResolvedValue) so each of the two panels'
    // GET calls gets its own Response -- a Response body can only be read
    // once, and LDAP/SAML fetch independently in parallel.
    vi.stubGlobal("fetch", vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ error: "internal error" }, 500))));
    renderPanel();

    expect(await screen.findAllByText("internal error")).toHaveLength(2);
  });
});
