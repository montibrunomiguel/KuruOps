import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FieldMappingTemplatesPanel } from "./FieldMappingTemplatesPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function templateFixture(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "tmpl1",
    name: "Wazuh fields",
    rules: [{ jsonPath: "rule.level", label: "Rule Level" }],
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function renderPanel() {
  return render(
    <AuthProvider>
      <FieldMappingTemplatesPanel />
    </AuthProvider>,
  );
}

describe("FieldMappingTemplatesPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("lists existing templates with their rule count and labels", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([templateFixture()])));
    renderPanel();

    expect(await screen.findByText("Wazuh fields")).toBeInTheDocument();
    expect(screen.getByText(/1 rule/)).toBeInTheDocument();
    expect(screen.getByText(/Rule Level/)).toBeInTheDocument();
  });

  it("shows the empty state with none created", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([])));
    renderPanel();

    expect(await screen.findByText("No field mapping templates yet.")).toBeInTheDocument();
  });

  it("creating a template posts name and non-blank rules only", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(templateFixture(), 201));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No field mapping templates yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Template" }));
    await userEvent.type(screen.getByLabelText("Name"), "Wazuh fields");
    await userEvent.type(screen.getByLabelText("JSON path"), "rule.level");
    await userEvent.type(screen.getByLabelText("Display label"), "Rule Level");

    // A second, never-filled-in row added via "+ Add rule" must be dropped
    // client-side before the request is sent.
    await userEvent.click(screen.getByRole("button", { name: "+ Add rule" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/field-mapping-templates", expect.objectContaining({ method: "POST" })),
    );
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "POST");
    const body = JSON.parse((postCall![1] as RequestInit).body as string);
    expect(body).toEqual({ name: "Wazuh fields", rules: [{ jsonPath: "rule.level", label: "Rule Level" }] });
  });

  it("editing an existing template PUTs the updated rules", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(templateFixture(), 200));
      return Promise.resolve(jsonResponse([templateFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Wazuh fields");

    await userEvent.click(screen.getByRole("button", { name: "Edit" }));
    const labelInput = screen.getByLabelText("Display label") as HTMLInputElement;
    await userEvent.clear(labelInput);
    await userEvent.type(labelInput, "Severity Level");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/field-mapping-templates/tmpl1",
        expect.objectContaining({ method: "PUT" }),
      ),
    );
    const putCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "PUT");
    const body = JSON.parse((putCall![1] as RequestInit).body as string);
    expect(body.rules).toEqual([{ jsonPath: "rule.level", label: "Severity Level" }]);
  });

  it("deleting requires an inline confirm click, and Cancel backs out without deleting", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/field-mapping-templates/tmpl1")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse([templateFixture()]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await screen.findByText("Wazuh fields");

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/field-mapping-templates/tmpl1", expect.anything());
    expect(screen.getByText(/Delete this template\?/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText(/Delete this template\?/)).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/field-mapping-templates/tmpl1", expect.objectContaining({ method: "DELETE" })),
    );
  });

  it("shows a validation error from the API", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(jsonResponse({ error: "template name is required" }, 400));
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No field mapping templates yet.")).toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "+ New Template" }));
    const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
    nameInput.removeAttribute("required");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("template name is required")).toBeInTheDocument();
  });
});
