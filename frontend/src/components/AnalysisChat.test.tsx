import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AnalysisChat } from "./AnalysisChat";
import { AuthProvider } from "../auth/AuthContext";
import type { AnalysisChatTranscript } from "../types/api";

// Captures the handler AnalysisChat registers with useEventStream so a test can
// push SSE events at it directly instead of standing up a real stream.
const stream = vi.hoisted(() => ({ handler: undefined as ((event: { type: string; data: unknown }) => void) | undefined }));
vi.mock("../api/eventStream", () => ({
  useEventStream: (handler: (event: { type: string; data: unknown }) => void) => {
    stream.handler = handler;
  },
}));

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

  it("a completed run that carries an error tells the analyst the last question wasn't answered", async () => {
    // What a failed follow-up leaves behind: the earlier report is restored and
    // the unanswered question dropped, with the provider's failure in `error`.
    const transcript: AnalysisChatTranscript = {
      runId: 7,
      status: "completed",
      error: "the AI provider is temporarily unavailable (503) -- try again in a moment",
      messages: [{ role: "assistant", content: "Alert Triage Report" }],
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(transcript)));
    renderChat();

    expect(await screen.findByText("Alert Triage Report")).toBeInTheDocument();
    expect(screen.getByText(/Your last question wasn't answered/)).toBeInTheDocument();
    expect(screen.getByText(/temporarily unavailable \(503\)/)).toBeInTheDocument();
  });

  it("a completed run without an error shows no failure banner", async () => {
    const transcript: AnalysisChatTranscript = {
      runId: 7,
      status: "completed",
      messages: [{ role: "assistant", content: "Alert Triage Report" }],
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(transcript)));
    renderChat();

    expect(await screen.findByText("Alert Triage Report")).toBeInTheDocument();
    expect(screen.queryByText(/wasn't answered/)).not.toBeInTheDocument();
  });

  it("refetches when the run's 'analyzed' event arrives, so a failed follow-up doesn't leave the chat on Thinking...", async () => {
    let finished = false;
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(
        jsonResponse(
          finished
            ? ({
                status: "completed",
                error: "the AI provider is temporarily unavailable (503)",
                messages: [{ role: "assistant", content: "Alert Triage Report" }],
              } satisfies AnalysisChatTranscript)
            : ({
                status: "running",
                messages: [
                  { role: "assistant", content: "Alert Triage Report" },
                  { role: "user", content: "which host?" },
                ],
              } satisfies AnalysisChatTranscript),
        ),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    renderChat();

    expect(await screen.findByText("Thinking...")).toBeInTheDocument();

    // A failed turn publishes no ai_analysis_turn event -- only this one.
    finished = true;
    act(() => stream.handler?.({ type: "alert", data: { id: "a1", action: "analyzed" } }));

    expect(await screen.findByText(/Your last question wasn't answered/)).toBeInTheDocument();
    expect(screen.queryByText("Thinking...")).not.toBeInTheDocument();
  });

  it("ignores 'analyzed' events for a different alert or a different action", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ messages: [] } satisfies AnalysisChatTranscript));
    vi.stubGlobal("fetch", fetchMock);
    renderChat();
    await screen.findByText(/Ask the assistant anything/);
    const callsAfterLoad = fetchMock.mock.calls.length;

    act(() => stream.handler?.({ type: "alert", data: { id: "someone-else", action: "analyzed" } }));
    act(() => stream.handler?.({ type: "alert", data: { id: "a1", action: "updated" } }));
    act(() => stream.handler?.({ type: "incident", data: { id: "a1", action: "analyzed" } }));

    expect(fetchMock.mock.calls.length).toBe(callsAfterLoad);
  });

  it("shows a provider failure in the interface language when the backend sends a code", async () => {
    const transcript: AnalysisChatTranscript = {
      runId: 7,
      status: "completed",
      error: "the AI provider is temporarily unavailable (503) -- try again in a moment",
      errorCode: "provider_unavailable",
      messages: [{ role: "assistant", content: "Alert Triage Report" }],
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(transcript)));
    renderChat();

    expect(await screen.findByText(/it may be overloaded/)).toBeInTheDocument();
    // The raw English hint is replaced, not shown next to the translation.
    expect(screen.queryByText(/503/)).not.toBeInTheDocument();
  });

  it("falls back to the raw error text for a code it has no translation for", async () => {
    const transcript: AnalysisChatTranscript = {
      runId: 7,
      status: "failed",
      error: "something new went wrong",
      errorCode: "provider_from_the_future",
      messages: [],
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(transcript)));
    renderChat();

    expect(await screen.findByText("something new went wrong")).toBeInTheDocument();
  });

  it("offers suggested questions on an empty chat, and clicking one sends it", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) =>
      Promise.resolve(init?.method === "POST" ? jsonResponse({ status: "running" }, 202) : jsonResponse({ messages: [] } satisfies AnalysisChatTranscript)),
    );
    vi.stubGlobal("fetch", fetchMock);
    renderChat();

    await userEvent.click(await screen.findByRole("button", { name: "Run a full triage of this item" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/analyze/messages",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ text: "Run a full triage of this item" }) }),
      ),
    );
  });

  describe("a follow-up that fails", () => {
    // The POST is accepted, the run goes to running, then the turn fails and the
    // backend restores the earlier report with the question dropped.
    function stubFailingFollowUp() {
      let phase: "idle" | "running" | "failed" = "idle";
      vi.stubGlobal(
        "fetch",
        vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
          if (init?.method === "POST") {
            phase = "running";
            return Promise.resolve(jsonResponse({ status: "running" }, 202));
          }
          const report = [{ role: "assistant" as const, content: "Alert Triage Report" }];
          const body: AnalysisChatTranscript =
            phase === "running"
              ? { status: "running", messages: report }
              : phase === "failed"
                ? { status: "completed", error: "the AI provider is temporarily unavailable (503)", errorCode: "provider_unavailable", messages: report }
                : { status: "completed", messages: report };
          return Promise.resolve(jsonResponse(body));
        }),
      );
      return () => {
        phase = "failed";
      };
    }

    it("puts the dropped question back in the input so it can be sent again", async () => {
      const failTurn = stubFailingFollowUp();
      renderChat();
      const input = await screen.findByPlaceholderText("Ask a follow-up question...");

      await userEvent.type(input, "which host?");
      await userEvent.click(screen.getByRole("button", { name: "Send" }));
      await waitFor(() => expect(input).toHaveValue(""));

      failTurn();
      act(() => stream.handler?.({ type: "alert", data: { id: "a1", action: "analyzed" } }));

      await waitFor(() => expect(input).toHaveValue("which host?"));
      expect(screen.getByText(/Your last question wasn't answered/)).toBeInTheDocument();
    });

    it("does not overwrite what the analyst has started typing in the meantime", async () => {
      const failTurn = stubFailingFollowUp();
      renderChat();
      const input = await screen.findByPlaceholderText("Ask a follow-up question...");

      await userEvent.type(input, "which host?");
      await userEvent.click(screen.getByRole("button", { name: "Send" }));
      await waitFor(() => expect(input).toHaveValue(""));
      // The run is still 'running', which disables the input; the analyst's
      // draft can only be typed once it is free again, i.e. after the failure.
      failTurn();
      act(() => stream.handler?.({ type: "alert", data: { id: "a1", action: "analyzed" } }));
      await waitFor(() => expect(input).toHaveValue("which host?"));

      await userEvent.clear(input);
      await userEvent.type(input, "something else");
      act(() => stream.handler?.({ type: "alert", data: { id: "a1", action: "analyzed" } }));
      await screen.findByText(/Your last question wasn't answered/);
      expect(input).toHaveValue("something else");
    });
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
