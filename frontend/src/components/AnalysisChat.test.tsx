import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AnalysisChat } from "./AnalysisChat";
import { AuthProvider } from "../auth/AuthContext";
import type { AnalysisChatTranscript } from "../types/api";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function renderChat(onClose: () => void = vi.fn()) {
  return render(
    <AuthProvider>
      <AnalysisChat contextType="alert" contextId="a1" onClose={onClose} />
    </AuthProvider>,
  );
}

describe("AnalysisChat", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows an empty-state message when no analysis has ever run", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ messages: [] } satisfies AnalysisChatTranscript)));
    renderChat();

    expect(await screen.findByText(/Ask the assistant anything/)).toBeInTheDocument();
  });

  it("renders an existing transcript's user/assistant messages", async () => {
    const transcript: AnalysisChatTranscript = {
      runId: 1,
      status: "completed",
      messages: [
        { role: "user", content: "is this worth escalating?" },
        { role: "assistant", content: "Looks like a routine scan." },
      ],
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(transcript)));
    renderChat();

    expect(await screen.findByText("is this worth escalating?")).toBeInTheDocument();
    expect(screen.getByText("Looks like a routine scan.")).toBeInTheDocument();
  });

  it("sending a message posts to analyze/messages and refetches the transcript", async () => {
    let posted = false;
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === "POST" && url === "/api/v1/alerts/a1/analyze/messages") {
        posted = true;
        return Promise.resolve(jsonResponse({ status: "running" }, 202));
      }
      if (!posted) return Promise.resolve(jsonResponse({ messages: [] } satisfies AnalysisChatTranscript));
      return Promise.resolve(
        jsonResponse({
          status: "running",
          messages: [{ role: "user", content: "what about the source IP?" }],
        } satisfies AnalysisChatTranscript),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    renderChat();

    await screen.findByText(/Ask the assistant anything/);
    await userEvent.type(screen.getByPlaceholderText("Ask a follow-up question..."), "what about the source IP?");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/analyze/messages",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ text: "what about the source IP?" }) }),
      ),
    );
    expect(await screen.findByText("what about the source IP?")).toBeInTheDocument();
  });

  it("a paused run shows an inline approve/reject prompt, and approving posts to the tool-call endpoint", async () => {
    let approved = false;
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url === "/api/v1/alerts/a1/analyze/tool-calls/42/approve") {
        approved = true;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      const transcript: AnalysisChatTranscript = approved
        ? { status: "completed", messages: [{ role: "assistant", content: "Host quarantined." }] }
        : {
            status: "paused",
            pendingToolCallId: 42,
            messages: [{ role: "assistant", content: "", toolCalls: [{ id: "call_1", name: "quarantine_host", args: {} }] }],
          };
      return Promise.resolve(jsonResponse(transcript));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderChat();

    expect(await screen.findByText(/wants to run a tool/)).toBeInTheDocument();
    expect(screen.getByText("Requesting tool: quarantine_host")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Approve" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/alerts/a1/analyze/tool-calls/42/approve", expect.objectContaining({ method: "POST" })),
    );
    expect(await screen.findByText("Host quarantined.")).toBeInTheDocument();
  });

  it("clicking the close button calls onClose", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ messages: [] } satisfies AnalysisChatTranscript)));
    const onClose = vi.fn();
    renderChat(onClose);

    await screen.findByText(/Ask the assistant anything/);
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalled();
  });
});
