import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DatabaseMigrationPanel } from "./DatabaseMigrationPanel";
import { AuthProvider } from "../../auth/AuthContext";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderPanel() {
  return render(
    <AuthProvider>
      <DatabaseMigrationPanel />
    </AuthProvider>,
  );
}

async function fillForm() {
  await userEvent.type(screen.getByLabelText("Host"), "target.example.com");
  const portInput = screen.getByLabelText("Port");
  await userEvent.clear(portInput);
  await userEvent.type(portInput, "5432");
  await userEvent.type(screen.getByLabelText("Database name"), "kuruops");
  await userEvent.type(screen.getByLabelText(/^User/), "postgres");
  await userEvent.type(screen.getByLabelText("Password"), "s3cret");
}

describe("DatabaseMigrationPanel", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("Test Connection posts the target config and shows success", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await fillForm();

    await userEvent.click(screen.getByRole("button", { name: "Test Connection" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/settings/database-migration/test-connection",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            host: "target.example.com", database: "kuruops", user: "postgres",
            password: "s3cret", sslMode: "disable", port: 5432,
          }),
        }),
      ),
    );
    expect(await screen.findByText("Connection succeeded.")).toBeInTheDocument();
  });

  it("shows the server's error message on a failed test connection", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "connect to target database: dial failed" }, 400)));
    renderPanel();
    await fillForm();

    await userEvent.click(screen.getByRole("button", { name: "Test Connection" }));
    expect(await screen.findByText("connect to target database: dial failed")).toBeInTheDocument();
  });

  it("Migrate & Switch requires an inline confirm before calling /migrate", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        schemaVersion: 29,
        rowCounts: { alerts: { source: 3, target: 3 }, incidents: { source: 1, target: 1 } },
        appDsn: "postgres://kuruops_app:xyz@target.example.com:5432/kuruops?sslmode=disable",
        workerDsn: "postgres://kuruops_worker:abc@target.example.com:5432/kuruops?sslmode=disable",
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await fillForm();

    await userEvent.click(screen.getByRole("button", { name: "Migrate & Switch" }));
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/settings/database-migration/migrate", expect.anything());

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/settings/database-migration/migrate", expect.objectContaining({ method: "POST" })),
    );

    expect(await screen.findByText("Migration complete")).toBeInTheDocument();
    expect(screen.getByText(/version 29/)).toBeInTheDocument();
    expect(screen.getByText("alerts")).toBeInTheDocument();
    expect(screen.getByDisplayValue("postgres://kuruops_app:xyz@target.example.com:5432/kuruops?sslmode=disable")).toBeInTheDocument();
  });

  it("shows the server's error message on a failed migration", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ error: "row count mismatch for alerts: copied 2 of 3 rows" }, 400));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel();
    await fillForm();

    await userEvent.click(screen.getByRole("button", { name: "Migrate & Switch" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    expect(await screen.findByText("row count mismatch for alerts: copied 2 of 3 rows")).toBeInTheDocument();
  });
});
