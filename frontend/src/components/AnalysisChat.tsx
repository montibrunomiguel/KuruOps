import { useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";
import { useEventStream } from "../api/eventStream";
import type { AnalysisChatMessage, AnalysisChatTranscript } from "../types/api";
import { renderMarkdown } from "../lib/markdown";
import { Modal } from "./Modal";

interface AnalysisChatProps {
  contextType: "alert" | "incident";
  contextId: string;
  onClose: () => void;
}

// AnalysisChat replaces the old one-shot "Analisar com IA" result block with
// an actual back-and-forth conversation, reusing the same agentic loop (and
// the same MCP tool-call approval mechanism) the auto-triggered
// analysis-on-ingest already runs -- see AIAnalysisService.Continue*Analysis
// on the backend. Opened as a modal from AlertDetailPage/IncidentDetailPage
// instead of the old always-visible static result panel; the automatic
// analysis triggered on ingest is unchanged, this only covers the manual
// button's behavior.
export function AnalysisChat({ contextType, contextId, onClose }: AnalysisChatProps) {
  const { t, i18n } = useTranslation();
  const { token } = useAuth();
  const basePath = contextType === "alert" ? "/api/v1/alerts" : "/api/v1/incidents";

  const [transcript, setTranscript] = useState<AnalysisChatTranscript | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [deciding, setDeciding] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  // The last follow-up question sent, kept until its turn is known to have been
  // answered. A follow-up that fails leaves the run completed with the question
  // dropped (see AIAnalysisService.restoreCompletedRun), so without this the
  // analyst's own text would be gone along with the answer they never got.
  const unansweredQuestion = useRef<string | null>(null);

  function applyTranscript(next: AnalysisChatTranscript) {
    setTranscript(next);
    const pending = unansweredQuestion.current;
    if (pending === null || next.status === "running" || next.status === "paused") return;
    unansweredQuestion.current = null;
    // Put the question back for a one-click retry -- but never over text the
    // analyst has started typing in the meantime.
    if (next.status === "completed" && next.error) setText((current) => current || pending);
  }

  async function reload() {
    try {
      applyTranscript(await api.get<AnalysisChatTranscript>(`${basePath}/${contextId}/analyze/messages`, token));
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  // Initial load. Written as a promise chain with a cancellation flag rather than
  // calling reload() so a slow response for a previous contextId can never
  // overwrite the transcript of the one now on screen.
  useEffect(() => {
    let cancelled = false;
    api
      .get<AnalysisChatTranscript>(`${basePath}/${contextId}/analyze/messages`, token)
      .then((next) => {
        if (!cancelled) applyTranscript(next);
      })
      .catch((err) => {
        if (!cancelled) setError(mutationErrorMessage(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // token is read once here on purpose: a token refresh must not refetch (and
    // flash) the open conversation, the event stream keeps it current.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contextId, basePath]);

  // Turn-by-turn incremental updates: driveAgentLoop publishes one of these
  // per model reply (see AIAnalysisService.publishTurn on the backend), so
  // the transcript refetches as the conversation happens instead of only
  // once the whole run finishes.
  //
  // ai_analysis_turn only fires when the model actually replies. A run that
  // ends in failure (a provider 503, say) never produces one, so the chat
  // would sit on "Thinking..." forever -- the "analyzed" event the backend
  // publishes when every run finishes, success or not (see notifyAnalyzed),
  // is what tells it to refetch and pick up the final status/error.
  useEventStream((event) => {
    if (event.type === "ai_analysis_turn") {
      const payload = event.data as { contextId?: string } | null;
      if (payload?.contextId === contextId) void reload();
      return;
    }
    if (event.type === contextType) {
      const payload = event.data as { id?: string; action?: string } | null;
      if (payload?.id === contextId && payload.action === "analyzed") void reload();
    }
  });

  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ behavior: "smooth" });
  }, [transcript?.messages.length, transcript?.status]);

  const busy = transcript?.status === "running" || transcript?.status === "paused";

  async function sendText(question: string) {
    if (!question.trim() || busy) return;
    setSending(true);
    setError(null);
    try {
      await api.post(`${basePath}/${contextId}/analyze/messages`, { text: question }, token);
      unansweredQuestion.current = question;
      setText("");
      await reload();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSending(false);
    }
  }

  function send(e: FormEvent) {
    e.preventDefault();
    void sendText(text);
  }

  // The backend's error text is English. For a provider failure it also sends a
  // stable code, which is shown in the interface language instead; anything
  // without a translation falls back to the raw text rather than to nothing.
  function runErrorText(tr: AnalysisChatTranscript): string {
    const key = `analysisChat.errors.${tr.errorCode ?? ""}`;
    return tr.errorCode && i18n.exists(key) ? t(key) : (tr.error ?? "");
  }

  async function decide(action: "approve" | "reject") {
    if (!transcript?.pendingToolCallId) return;
    setDeciding(true);
    setError(null);
    try {
      await api.post(`${basePath}/${contextId}/analyze/tool-calls/${transcript.pendingToolCallId}/${action}`, {}, token);
      await reload();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setDeciding(false);
    }
  }

  return (
    <Modal onClose={onClose} label={t("analysisChat.title")} style={{ maxWidth: 640, display: "flex", flexDirection: "column", height: "min(680px, 85vh)" }}>
      <div className="panel-header">
        <h2 className="modal-title" style={{ marginBottom: 0 }}>
          {t("analysisChat.title")}
        </h2>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label={t("common.close")}>
          ×
        </button>
      </div>

      <div style={{ flex: 1, overflowY: "auto", display: "flex", flexDirection: "column", gap: 10, padding: "6px 2px" }}>
        {loading && <div className="empty-state">{t("common.loading")}</div>}

        {!loading && transcript && transcript.messages.length === 0 && !busy && (
          <div className="empty-state">
            <p>{t("analysisChat.empty")}</p>
            {/* A blank box asks the analyst to invent a prompt; these are the questions
                that are nearly always worth asking first. */}
            <p className="helper-text">{t("analysisChat.suggestionsLabel")}</p>
            <div className="row-actions" style={{ justifyContent: "center", flexWrap: "wrap" }}>
              {(["triage", "nextSteps", "related"] as const).map((key) => (
                <button
                  key={key}
                  type="button"
                  className="btn btn-ghost btn-sm"
                  disabled={sending}
                  onClick={() => void sendText(t(`analysisChat.suggestions.${key}`))}
                >
                  {t(`analysisChat.suggestions.${key}`)}
                </button>
              ))}
            </div>
          </div>
        )}

        {transcript?.messages.map((m, idx) => <ChatBubble key={idx} message={m} />)}

        {transcript?.status === "running" && <p className="helper-text">{t("analysisChat.thinking")}</p>}

        {transcript?.status === "paused" && transcript.pendingToolCallId != null && (
          <div className="panel" style={{ borderColor: "var(--accent)", marginBottom: 0 }}>
            <p className="row-title" style={{ marginBottom: 8 }}>
              {t("analysisChat.approvalNeeded")}
            </p>
            <div className="row-actions">
              <button className="btn btn-primary btn-sm" disabled={deciding} onClick={() => decide("approve")}>
                {t("settings.mcp.approvals.approve")}
              </button>
              <button className="btn btn-danger btn-sm" disabled={deciding} onClick={() => decide("reject")}>
                {t("settings.mcp.approvals.reject")}
              </button>
            </div>
          </div>
        )}

        {transcript?.status === "failed" && transcript.error && (
          <div className="error-banner">{runErrorText(transcript)}</div>
        )}

        {/* A failed follow-up leaves the run 'completed' (the earlier report is
            kept, the unanswered question is dropped) with the cause in error --
            without this the question just vanishes and nothing says why. */}
        {transcript?.status === "completed" && transcript.error && (
          <div className="error-banner">
            {t("analysisChat.followUpFailed")} {runErrorText(transcript)}
          </div>
        )}

        <div ref={bottomRef} />
      </div>

      {error && <div className="error-banner">{error}</div>}

      <form onSubmit={send} style={{ display: "flex", gap: 8, marginTop: 12 }}>
        <input
          className="input"
          style={{ flex: 1 }}
          placeholder={t("analysisChat.placeholder")}
          value={text}
          onChange={(e) => setText(e.target.value)}
          disabled={sending || busy}
        />
        <button type="submit" className="btn btn-primary btn-sm" disabled={sending || busy || !text.trim()}>
          {sending ? t("analysisChat.sending") : t("analysisChat.send")}
        </button>
      </form>
    </Modal>
  );
}

function ChatBubble({ message }: { message: AnalysisChatMessage }) {
  const { t } = useTranslation();

  // Tool results are internal plumbing (a JSON blob the model reads, not
  // something an analyst wrote or would normally read) -- shown as a small
  // muted block rather than a chat bubble, same spirit as PendingApprovalRow's
  // args preview in Settings -> MCP Servers.
  if (message.role === "tool") {
    return (
      <pre
        className="mono"
        style={{
          margin: 0,
          fontSize: 11,
          color: "var(--text-muted)",
          background: "var(--surface-2)",
          padding: "6px 10px",
          borderRadius: 6,
          whiteSpace: "pre-wrap",
          overflowX: "auto",
        }}
      >
        {message.content}
      </pre>
    );
  }

  const isUser = message.role === "user";
  const requestedTool = message.toolCalls && message.toolCalls.length > 0 ? message.toolCalls[0].name : null;

  return (
    <div style={{ display: "flex", flexDirection: "column", alignItems: isUser ? "flex-end" : "flex-start" }}>
      {message.content && (
        <div
          style={{
            maxWidth: "85%",
            padding: "8px 12px",
            borderRadius: 10,
            background: isUser ? "var(--accent)" : "var(--surface-2)",
            color: isUser ? "#fff" : "var(--text)",
            fontSize: 13,
            whiteSpace: isUser ? "pre-wrap" : "normal",
          }}
        >
          {isUser ? message.content : renderMarkdown(message.content)}
        </div>
      )}
      {requestedTool && (
        <span className="helper-text" style={{ marginTop: 4 }}>
          {t("analysisChat.requestingTool", { tool: requestedTool })}
        </span>
      )}
    </div>
  );
}
