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
  const { t } = useTranslation();
  const { token } = useAuth();
  const basePath = contextType === "alert" ? "/api/v1/alerts" : "/api/v1/incidents";

  const [transcript, setTranscript] = useState<AnalysisChatTranscript | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [deciding, setDeciding] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);

  async function reload() {
    try {
      const next = await api.get<AnalysisChatTranscript>(`${basePath}/${contextId}/analyze/messages`, token);
      setTranscript(next);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contextId]);

  // Turn-by-turn incremental updates: driveAgentLoop publishes one of these
  // per model reply (see AIAnalysisService.publishTurn on the backend), so
  // the transcript refetches as the conversation happens instead of only
  // once the whole run finishes.
  useEventStream((event) => {
    if (event.type !== "ai_analysis_turn") return;
    const payload = event.data as { contextId?: string } | null;
    if (payload?.contextId === contextId) void reload();
  });

  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ behavior: "smooth" });
  }, [transcript?.messages.length, transcript?.status]);

  const busy = transcript?.status === "running" || transcript?.status === "paused";

  async function send(e: FormEvent) {
    e.preventDefault();
    if (!text.trim() || busy) return;
    setSending(true);
    setError(null);
    try {
      await api.post(`${basePath}/${contextId}/analyze/messages`, { text }, token);
      setText("");
      await reload();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSending(false);
    }
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
    <Modal onClose={onClose} style={{ maxWidth: 640, display: "flex", flexDirection: "column", height: "min(680px, 85vh)" }}>
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
          <div className="empty-state">{t("analysisChat.empty")}</div>
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
          <div className="error-banner">{transcript.error}</div>
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
