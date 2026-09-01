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

  it("the auto-analyze checkbox defaults unchecked and is included as false in the payload", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(providerFixture(), 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No LLM provider registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Provider" }));
    const checkbox = screen.getByLabelText("Automatically analyze every incoming alert") as HTMLInputElement;
    expect(checkbox.checked).toBe(false);

    await userEvent.type(screen.getByLabelText("Name"), "Claude");
    await userEvent.type(screen.getByLabelText("Model"), "claude-opus");
    await userEvent.type(screen.getByLabelText("API Key"), "sk-secret");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "POST");
      expect(postCall).toBeDefined();
      const body = JSON.parse((postCall![1] as RequestInit).body as string);
      expect(body.autoAnalyzeAllAlerts).toBe(false);
    });
  });

  it("checking the auto-analyze checkbox sends true in the payload", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(providerFixture(), 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No LLM provider registered yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Provider" }));
    await userEvent.click(screen.getByLabelText("Automatically analyze every incoming alert"));
    await userEvent.type(screen.getByLabelText("Name"), "Claude");
    await userEvent.type(screen.getByLabelText("Model"), "claude-opus");
    await userEvent.type(screen.getByLabelText("API Key"), "sk-secret");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "POST");
      expect(postCall).toBeDefined();
      const body = JSON.parse((postCall![1] as RequestInit).body as string);
      expect(body.autoAnalyzeAllAlerts).toBe(true);
    });
  });

  it("shows the auto-analyze badge only for a provider that opted in", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse([providerFixture({ isDefault: true, autoAnalyzeAllAlerts: true })])),
    );
    renderPanel();

    expect(await screen.findByText("OpenAI")).toBeInTheDocument();
    expect(screen.getByText("auto-analyzes all alerts")).toBeInTheDocument();
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

  it("Edit seeds the form from the provider and saves it with PUT", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(providerFixture()));
      return Promise.resolve(jsonResponse([providerFixture({ name: "OpenAI", model: "gpt-4o" })]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));

    // Seeded from the row, not blank -- the whole point of edit.
    expect(screen.getByLabelText("Name")).toHaveValue("OpenAI");
    expect(screen.getByLabelText("Model")).toHaveValue("gpt-4o");
    expect(screen.getByLabelText("Base URL")).toHaveValue("https://api.openai.com/v1");

    await userEvent.clear(screen.getByLabelText("Model"));
    await userEvent.type(screen.getByLabelText("Model"), "gpt-4o-mini");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const put = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "PUT");
      expect(put).toBeDefined();
      expect(put![0]).toContain("/api/v1/settings/llm-providers/p1");
      expect(JSON.parse((put![1] as RequestInit).body as string).model).toBe("gpt-4o-mini");
    });
  });

  it("the API key is optional when editing, so an untouched key is kept", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(providerFixture()));
      return Promise.resolve(jsonResponse([providerFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    // Required on create, deliberately not on edit -- the stored key never
    // round-trips to the browser, so there is nothing to prefill it with.
    expect(screen.getByLabelText("API Key")).not.toBeRequired();

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const put = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "PUT");
      expect(put).toBeDefined();
      expect(JSON.parse((put![1] as RequestInit).body as string).apiKey).toBe("");
    });
  });

  it("editing replaces that row and Cancel brings it back", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([providerFixture()])));
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await screen.findByRole("button", { name: "Edit" })).toBeInTheDocument();
  });

});
