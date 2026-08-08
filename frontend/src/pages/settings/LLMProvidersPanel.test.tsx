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

  it("Cancel on the delete confirmation dismisses it without deleting", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([providerFixture()]));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("OpenAI");

    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByRole("button", { name: "Cancel" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("button", { name: "Confirm delete" })).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/llm-providers/p1", expect.anything());
  });

  it("shows the error banner when the initial list fetch fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "list boom" }, 500)));
    renderPanel();

    expect(await screen.findByText("list boom")).toBeInTheDocument();
  });

  it("the Cancel button on the create form dismisses it without submitting", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([]));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No LLM provider registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Provider" }));
    expect(screen.getByLabelText("Name")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByLabelText("Name")).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/llm-providers", expect.objectContaining({ method: "POST" }));
  });

  it("shows an error message when creating a provider fails", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ error: "create boom" }, 400));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No LLM provider registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Provider" }));
    await userEvent.type(screen.getByLabelText("Name"), "Claude");
    await userEvent.type(screen.getByLabelText("Model"), "claude-opus");
    await userEvent.type(screen.getByLabelText("API Key"), "sk-secret");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("create boom")).toBeInTheDocument();
  });

  it("submitting with a non-anthropic kind includes the typed baseUrl in the payload", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(providerFixture(), 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No LLM provider registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Provider" }));
    await userEvent.click(screen.getByRole("button", { name: "OpenAI-compatible" }));
    await userEvent.type(screen.getByLabelText("Name"), "Local Proxy");
    await userEvent.type(screen.getByLabelText("Model"), "gpt-4o");
    await userEvent.type(screen.getByLabelText("Base URL"), "https://proxy.internal/v1");
    await userEvent.type(screen.getByLabelText("API Key"), "sk-secret");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "POST");
      expect(postCall).toBeDefined();
      const body = JSON.parse((postCall![1] as RequestInit).body as string);
      expect(body.baseUrl).toBe("https://proxy.internal/v1");
    });
  });

  it("shows an error message when Set default fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        if (url.includes("/default")) return Promise.resolve(jsonResponse({ error: "default boom" }, 500));
        return Promise.resolve(jsonResponse([providerFixture()]));
      }),
    );
    renderPanel();
    await screen.findByText("OpenAI");

    await userEvent.click(screen.getByRole("button", { name: "Set default" }));
    expect(await screen.findByText("default boom")).toBeInTheDocument();
  });

  it("shows an error message when Remove fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
        if (init?.method === "DELETE") return Promise.resolve(jsonResponse({ error: "delete boom" }, 500));
        return Promise.resolve(jsonResponse([providerFixture()]));
      }),
    );
    renderPanel();
    await screen.findByText("OpenAI");

    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    expect(await screen.findByText("delete boom")).toBeInTheDocument();
  });

  it("omits the baseUrl segment from the row subtitle when the provider has none", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse([providerFixture({ kind: "anthropic", baseUrl: undefined })])),
    );
    renderPanel();

    const row = (await screen.findByText("OpenAI")).closest(".row")!;
    expect(row).not.toHaveTextContent("·  ·");
    expect(row.textContent).not.toContain("https://");
  });
});
