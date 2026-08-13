import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import type { Alert, AlertComment } from "../../types/alerts";
import type { Playbook } from "../../types/playbooks";
import { SeverityBadge, AlertStatusBadge, ClassificationBadge } from "../../components/badges";
import { TagPicker } from "../../components/TagPicker";
import { AttachmentButton, AttachmentPreview } from "../../components/AttachmentButton";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { SparkleIcon } from "../../components/icons";
import { formatDateTime, initials, shortId } from "../../lib/format";
import { MetadataPanel } from "./AlertDetailPage/MetadataPanel";
import { PayloadPanel } from "./AlertDetailPage/PayloadPanel";
import { ClassificationPanel } from "./AlertDetailPage/ClassificationPanel";
import { CloseAlertModal } from "./AlertDetailPage/CloseAlertModal";
import { LinkedAlertsPanel } from "./AlertDetailPage/LinkedAlertsPanel";
import { AssigneePanel } from "./AlertDetailPage/AssigneePanel";
import { SeverityOverridePanel } from "./AlertDetailPage/SeverityOverridePanel";

export function AlertDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const { token } = useAuth();
  const navigate = useNavigate();

  const { data: alert, loading, error, reload } = useList<Alert>(
    async (tk) => {
      const a = await api.get<Alert>(`/api/v1/alerts/${id}`, tk);
      return [a];
    },
    [id],
  );
  const current = alert?.[0];

  // Analysis runs in the background on the server (POST /analyze returns
  // 202 immediately, see AIAnalysisService's doc comment) -- this is what
  // tells the page to refetch once it's done, instead of waiting on the
  // POST response the way it used to. Filtered to this alert's id: other
  // alerts changing shouldn't reload this page.
  useEventStream((event) => {
    if (event.type !== "alert") return;
    const payload = event.data as { id?: string } | null;
    if (payload?.id === id) reload();
  });

  const { data: comments, reload: reloadComments } = useList<AlertComment>(
    (tk) => api.get<AlertComment[]>(`/api/v1/alerts/${id}/comments`, tk),
    [id],
  );

  const { data: playbookMatch } = useList<Playbook>(
    async (tk) => {
      if (!current) return [];
      const pb = await api.get<Playbook | null>(
        `/api/v1/playbooks/match?title=${encodeURIComponent(current.title)}`,
        tk,
      );
      return pb ? [pb] : [];
    },
    [current?.title],
  );

  const [actionError, setActionError] = useState<string | null>(null);
  const [escalating, setEscalating] = useState(false);
  const [showCloseModal, setShowCloseModal] = useState(false);

  // No local "analyzing" flag -- current.latestAnalysisStatus (from the
  // most recent GET) already reflects "running" the instant the POST
  // below returns, since the server creates the run row synchronously
  // before responding 202. reload() picks that up immediately; the
  // eventual completed/failed transition arrives via the SSE subscription
  // above.
  async function analyze() {
    if (!id) return;
    setActionError(null);
    try {
      await api.post<{ status: string }>(`/api/v1/alerts/${id}/analyze`, {}, token);
      reload();
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    }
  }

  async function escalate() {
    if (!id) return;
    setEscalating(true);
    setActionError(null);
    try {
      const res = await api.post<{ incidentId: string }>(`/api/v1/alerts/${id}/escalate`, {}, token);
      navigate(`/incidents/${res.incidentId}`);
    } catch (err) {
      setActionError(mutationErrorMessage(err));
      setEscalating(false);
    }
  }

  if (loading) return <div className="empty-state">{t("common.loading")}</div>;
  if (error) return <div className="error-banner">{error}</div>;
  if (!current) return <div className="empty-state">{t("alerts.detail.notFound")}</div>;

  return (
    <div>
      <div className="toolbar">
        <Link to="/alerts" className="back-link" style={{ marginBottom: 0 }}>
          ‹ {t("alerts.detail.backToAlerts")}
        </Link>
        <WebhookStatusIndicator />
      </div>

      <div className="detail-header">
        <div>
          <div className="detail-header-badges" style={{ marginTop: 0, marginBottom: 8 }}>
            <span className="mono" style={{ color: "var(--text-muted)", fontSize: 12.5 }}>
              #{shortId(current.id)}
            </span>
            <SeverityBadge severity={current.severity} />
            <AlertStatusBadge status={current.status} />
            {current.classification && <ClassificationBadge classification={current.classification} />}
          </div>
          <h1 className="page-title" style={{ marginBottom: 4 }}>
            {current.title}
          </h1>
          <p className="page-sub" style={{ marginBottom: 0 }}>
            {t("alerts.detail.receivedLine", { source: current.source, date: formatDateTime(current.receivedAt) })}
          </p>
        </div>
        <div className="toolbar-actions">
          <button
            className="btn btn-primary btn-sm"
            disabled={current.latestAnalysisStatus === "running" || current.latestAnalysisStatus === "paused"}
            onClick={analyze}
          >
            <SparkleIcon width={14} height={14} />
            {current.latestAnalysisStatus === "running" || current.latestAnalysisStatus === "paused"
              ? t("alerts.detail.analyzing")
              : t("alerts.detail.analyzeWithAI")}
          </button>
          {!(current.status === "closed" && current.classification) && (
            <button className="btn btn-sm" onClick={() => setShowCloseModal(true)}>
              {t("alerts.detail.closeAndClassify")}
            </button>
          )}
          {current.status !== "escalated" && current.status !== "closed" && (
            <button className="btn btn-danger btn-sm" disabled={escalating} onClick={escalate}>
              {escalating ? t("alerts.detail.escalating") : t("alerts.detail.escalateToIncident")}
            </button>
          )}
        </div>
      </div>

      {showCloseModal && (
        <CloseAlertModal
          alert={current}
          onClose={() => setShowCloseModal(false)}
          onSaved={() => {
            setShowCloseModal(false);
            reload();
          }}
        />
      )}

      <AlertTagsRow current={current} onSaved={reload} />

      {actionError && <div className="error-banner">{actionError}</div>}

      {(current.latestAnalysisStatus === "running" || current.latestAnalysisStatus === "paused") && (
        <div className="panel" style={{ marginBottom: 16, borderColor: "var(--accent)" }}>
          <h2 className="panel-title" style={{ marginBottom: 10 }}>
            {t("alerts.detail.aiResultTitle")}
          </h2>
          <p style={{ margin: 0, fontSize: 13, color: "var(--text-muted)" }}>{t("alerts.detail.aiRunningMessage")}</p>
        </div>
      )}

      {current.latestAnalysisStatus === "failed" && (
        <div className="panel" style={{ marginBottom: 16, borderColor: "var(--critical)" }}>
          <h2 className="panel-title" style={{ marginBottom: 10 }}>
            {t("alerts.detail.aiFailedTitle")}
          </h2>
          <p style={{ margin: 0, fontSize: 13, whiteSpace: "pre-wrap" }}>{current.latestAnalysisError}</p>
          <button className="btn btn-sm" style={{ marginTop: 10 }} onClick={analyze}>
            {t("alerts.detail.aiRetry")}
          </button>
        </div>
      )}

      {current.latestAnalysisStatus === "completed" && current.latestAnalysis && (
        <div className="panel" style={{ marginBottom: 16, borderColor: "var(--accent)" }}>
          <h2 className="panel-title" style={{ marginBottom: 10 }}>
            {t("alerts.detail.aiResultTitle")}
          </h2>
          <p style={{ margin: 0, fontSize: 13, whiteSpace: "pre-wrap" }}>{current.latestAnalysis}</p>
        </div>
      )}

      <div className="detail-layout">
        <div className="detail-main">
          <MetadataPanel metadata={current.metadata} />
          <PayloadPanel payload={current.payload} />
          <ClassificationPanel alert={current} />
          <LinkedAlertsPanel alertId={current.id} />

          <div className="panel">
            <h2 className="panel-title" style={{ marginBottom: 10 }}>
              {t("alerts.detail.teamNotesTitle")}
            </h2>
            {comments && comments.length === 0 && <div className="empty-state">{t("alerts.detail.noNotes")}</div>}
            {comments &&
              comments.map((c) => (
                <div className="comment-item" key={c.id}>
                  <div className="comment-item-row">
                    <span className="comment-avatar">{initials(c.authorName)}</span>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div className="comment-head">
                        <span style={{ fontWeight: 600, color: "var(--text)" }}>{c.authorName}</span>
                        <span>{formatDateTime(c.createdAt)}</span>
                      </div>
                      <p className="comment-body">{c.body}</p>
                      {c.attachmentUrl && <AttachmentPreview url={c.attachmentUrl} />}
                    </div>
                  </div>
                </div>
              ))}
            <AddAlertCommentForm alertId={current.id} onAdded={reloadComments} />
          </div>
        </div>

        <div className="detail-side">
          {playbookMatch && playbookMatch.length > 0 && (
            <div className="panel">
              <h2 className="panel-title" style={{ marginBottom: 10 }}>
                {t("alerts.detail.relatedPlaybookTitle")}
              </h2>
              <span className="badge badge-admin" style={{ marginBottom: 8, display: "inline-block" }}>
                {playbookMatch[0].category}
              </span>
              <p style={{ margin: "0 0 12px", fontSize: 13, fontWeight: 600 }}>{playbookMatch[0].title}</p>
              <Link to={`/playbooks/${playbookMatch[0].id}`} className="btn btn-sm" style={{ width: "100%", justifyContent: "center" }}>
                {t("alerts.detail.viewPlaybook")}
              </Link>
            </div>
          )}

          <AssigneePanel alert={current} onSaved={reload} />

          <SeverityOverridePanel alert={current} onSaved={reload} />

          <div className="panel">
            <h2 className="panel-title" style={{ marginBottom: 12 }}>
              {t("alerts.detail.metadataTitle")}
            </h2>
            <div className="meta-list">
              {current.ruleId && (
                <div className="meta-row">
                  <span className="meta-row-label">{t("alerts.detail.ruleId")}</span>
                  <span className="meta-row-value mono">{current.ruleId}</span>
                </div>
              )}
              {current.asset && (
                <div className="meta-row">
                  <span className="meta-row-label">{t("alerts.detail.asset")}</span>
                  <span className="meta-row-value mono">{current.asset}</span>
                </div>
              )}
              {current.srcIp && (
                <div className="meta-row">
                  <span className="meta-row-label">{t("alerts.detail.sourceIp")}</span>
                  <span className="meta-row-value mono">{current.srcIp}</span>
                </div>
              )}
            </div>
            {current.incidentId && (
              <>
                <hr className="section-divider" />
                <button className="btn btn-sm" style={{ width: "100%" }} onClick={() => navigate(`/incidents/${current.incidentId}`)}>
                  {t("alerts.detail.viewLinkedIncident")}
                </button>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function AddAlertCommentForm({ alertId, onAdded }: { alertId: string; onAdded: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [body, setBody] = useState("");
  const [attachmentUrl, setAttachmentUrl] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!body.trim()) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.post(`/api/v1/alerts/${alertId}/comments`, { body, attachmentUrl }, token);
      setBody("");
      setAttachmentUrl(null);
      onAdded();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} style={{ marginTop: 14, display: "flex", gap: 8, alignItems: "flex-start" }}>
      {error && <div className="error-banner">{error}</div>}
      <input
        className="input"
        style={{ flex: 1 }}
        placeholder={t("alerts.detail.addNotePlaceholder")}
        value={body}
        onChange={(e) => setBody(e.target.value)}
      />
      <AttachmentButton kind="alert" id={alertId} value={attachmentUrl} onChange={setAttachmentUrl} disabled={submitting} />
      <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
        {submitting ? t("alerts.detail.posting") : t("alerts.detail.post")}
      </button>
    </form>
  );
}

function AlertTagsRow({ current, onSaved }: { current: Alert; onSaved: () => void }) {
  const { token } = useAuth();
  const { t } = useTranslation();
  const [tags, setTags] = useState<string[]>(current.tags);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const dirty = tags.length !== current.tags.length || tags.some((tg) => !current.tags.includes(tg));

  async function save(next: string[]) {
    setSubmitting(true);
    setError(null);
    try {
      await api.put(`/api/v1/alerts/${current.id}/tags`, { tags: next }, token);
      onSaved();
    } catch (err) {
      setTags(current.tags);
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div style={{ marginBottom: 16 }}>
      <TagPicker
        value={tags}
        onChange={(next) => {
          setTags(next);
          void save(next);
        }}
        disabled={submitting}
      />
      {dirty && submitting && <span className="helper-text">{t("common.saving")}</span>}
      {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
    </div>
  );
}
