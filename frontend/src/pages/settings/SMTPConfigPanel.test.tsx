import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SMTPConfigPanel } from "./SMTPConfigPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <SMTPConfigPanel />
    </AuthProvider>,
  );
}

const configured = {
  host: "smtp.example.com",
  port: 587,
  useTls: true,
  username: "smtp-user",
  fromAddress: "no-reply@example.com",
  fromName: "ArgusOps",
};

describe("SMTPConfigPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows 'Configure' and no test-email section when nothing is set up yet", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(null)));
    renderPanel();

    expect(await screen.findByRole("button", { name: "Configure" })).toBeInTheDocument();
    expect(screen.getByLabelText("Host")).toBeInTheDocument();
    expect(screen.queryByText("Configured")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove configuration" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send test email" })).not.toBeInTheDocument();
  });

  it("shows 'Update', a badge, and the test-email section once configured", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(configured)));
    renderPanel();

    expect(await screen.findByRole("button", { name: "Update" })).toBeInTheDocument();
    expect(screen.getByText("Configured")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove configuration" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send test email" })).toBeInTheDocument();
    expect(screen.getByLabelText("Host")).toHaveValue("smtp.example.com");
  });

  it("saving the form PUTs the config and shows a success message", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(null));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.type(await screen.findByLabelText("Host"), "smtp.example.com");
    await userEvent.type(screen.getByLabelText(/From address/), "no-reply@example.com");
    await userEvent.click(screen.getByRole("button", { name: "Configure" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/smtp", expect.objectContaining({ method: "PUT" })),
    );
    expect(await screen.findByText("Configuration saved.")).toBeInTheDocument();
  });

  it("sending a test email POSTs to the test endpoint and shows the result", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST" && url.endsWith("/test")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(configured));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.type(await screen.findByPlaceholderText("Send to"), "someone@example.com");
    await userEvent.click(screen.getByRole("button", { name: "Send test email" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/smtp/test", expect.objectContaining({ method: "POST" })),
    );
    expect(await screen.findByText("Test email sent to someone@example.com.")).toBeInTheDocument();
  });

  it("removing the configuration DELETEs and clears the configured badge", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(configured));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Remove configuration" }));
    await userEvent.click(await screen.findByRole("button", { name: "Confirm delete" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/smtp", expect.objectContaining({ method: "DELETE" })),
    );
    expect(screen.queryByText("Configured")).not.toBeInTheDocument();
  });

  it("a fetch error surfaces the error banner", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "internal error" }, 500)));
    renderPanel();

    expect(await screen.findByText("internal error")).toBeInTheDocument();
  });
});
