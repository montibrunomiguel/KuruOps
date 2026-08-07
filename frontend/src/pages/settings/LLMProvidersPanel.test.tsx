import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LLMProvidersPanel } from "./LLMProvidersPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function providerFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return { id: "p1", name: "OpenAI", kind: "openai_compatible", model: "gpt-4o", baseUrl: "https://api.openai.com/v1", isDefault: false, ...overrides };
}

function renderPanel() {
  return render(
    <AuthProvider>
      <LLMProvidersPanel />
    </AuthProvider>,
  );
}

describe("LLMProvidersPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("lists providers, flagging the default one", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([providerFixture({ isDefault: true })])));
    renderPanel();

    expect(await screen.findByText("OpenAI")).toBeInTheDocument();
    expect(screen.getByText("default")).toBeInTheDocument();
  });

  it("the base URL field only shows for a non-anthropic kind", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderPanel();
    await waitFor(() => expect(screen.getByText("No LLM provider registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Provider" }));
    expect(screen.queryByLabelText("Base URL")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "OpenAI-compatible" }));
    expect(screen.getByLabelText("Base URL")).toBeInTheDocument();
  });

  it("creating a provider posts the payload and never round-trips the key visibly by default", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(providerFixture(), 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No LLM provider registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Provider" }));
    await userEvent.type(screen.getByLabelText("Name"), "Claude");
    await userEvent.type(screen.getByLabelText("Model"), "claude-opus");
    const apiKeyInput = screen.getByLabelText("API Key") as HTMLInputElement;
    expect(apiKeyInput.type).toBe("password");
    await userEvent.type(apiKeyInput, "sk-secret");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/llm-providers", expect.objectContaining({ method: "POST" })),
    );
  });

  it("Show toggles the API key field to plain text", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderPanel();
    await waitFor(() => expect(screen.getByText("No LLM provider registered yet.")).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "+ New Provider" }));

    const apiKeyInput = screen.getByLabelText("API Key") as HTMLInputElement;
    await userEvent.click(screen.getByRole("button", { name: "Show" }));
    expect(apiKeyInput.type).toBe("text");
  });

  it("Set default posts to the default endpoint, hidden for the already-default provider", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/default")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([providerFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("OpenAI");

    await userEvent.click(screen.getByRole("button", { name: "Set default" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/llm-providers/p1/default", expect.objectContaining({ method: "POST" })),
    );
  });

  it("no Set default button for the already-default provider", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([providerFixture({ isDefault: true })])));
    renderPanel();
    await screen.findByText("OpenAI");

    expect(screen.queryByRole("button", { name: "Set default" })).not.toBeInTheDocument();
  });

  it("Remove requires an inline confirm click before deleting", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/llm-providers/p1")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([providerFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("OpenAI");

    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/llm-providers/p1", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/llm-providers/p1", expect.objectContaining({ method: "DELETE" })),
    );
  });
});
