import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuthProvider } from "../auth/AuthContext";
import { AddNoteForm } from "./AddNoteForm";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderForm(kind: "alert" | "incident", onAdded: () => void) {
  return render(
    <AuthProvider>
      <AddNoteForm kind={kind} id="target-1" onAdded={onAdded} />
    </AuthProvider>,
  );
}

describe("AddNoteForm", () => {
  it("posts to /api/v1/alerts/:id/comments for kind='alert' and clears the input on success", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ id: "c1" }, 201));
    vi.stubGlobal("fetch", fetchMock);
    const onAdded = vi.fn();
    renderForm("alert", onAdded);

    const input = screen.getByPlaceholderText("Add a note for the team...");
    await userEvent.type(input, "Looks like a false positive");
    await userEvent.click(screen.getByRole("button", { name: "Post" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/target-1/comments",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    await waitFor(() => expect(onAdded).toHaveBeenCalled());
    expect(input).toHaveValue("");
  });

  it("posts to /api/v1/incidents/:id/comments for kind='incident'", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ id: "c1" }, 201));
    vi.stubGlobal("fetch", fetchMock);
    const onAdded = vi.fn();
    renderForm("incident", onAdded);

    await userEvent.type(screen.getByPlaceholderText("Add a note for the team..."), "Escalated to on-call");
    await userEvent.click(screen.getByRole("button", { name: "Post" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/incidents/target-1/comments",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });

  it("does not submit an empty/whitespace-only note", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    renderForm("alert", vi.fn());

    await userEvent.type(screen.getByPlaceholderText("Add a note for the team..."), "   ");
    await userEvent.click(screen.getByRole("button", { name: "Post" }));

    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("shows the server's error message on a failed post", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "note too long" }, 400)));
    renderForm("alert", vi.fn());

    await userEvent.type(screen.getByPlaceholderText("Add a note for the team..."), "a note the backend rejects");
    await userEvent.click(screen.getByRole("button", { name: "Post" }));

    expect(await screen.findByText("note too long")).toBeInTheDocument();
  });
});
