import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import type { Alert, AlertComment } from "../../types/alerts";
import { SeverityBadge, AlertStatusBadge, ClassificationBadge } from "../../components/badges";
import { AutoSaveTagPicker } from "../../components/AutoSaveTagPicker";
import { AttachmentPreview } from "../../components/AttachmentButton";
import { AddNoteForm } from "../../components/AddNoteForm";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { AnalysisChat } from "../../components/AnalysisChat";
import { SparkleIcon } from "../../components/icons";
import { AlertDetailSkeleton } from "../../components/AlertDetailSkeleton";
import { formatDateTime, initials, shortId } from "../../lib/format";
import { MetadataPanel } from "./AlertDetailPage/MetadataPanel";
import { PayloadPanel } from "./AlertDetailPage/PayloadPanel";
import { ClassificationPanel } from "./AlertDetailPage/ClassificationPanel";
import { CloseAlertModal } from "./AlertDetailPage/CloseAlertModal";
import { LinkedAlertsPanel } from "./AlertDetailPage/LinkedAlertsPanel";
import { AssigneePanel } from "./AlertDetailPage/AssigneePanel";
import { SeverityOverridePanel } from "./AlertDetailPage/SeverityOverridePanel";
import { PlaybookViewModal } from "../playbooks/PlaybookViewModal";

export function AlertDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const { token, user } = useAuth();
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

  const [actionError, setActionError] = useState<string | null>(null);
  const [escalating, setEscalating] = useState(false);
  const [startingInvestigation, setStartingInvestigation] = useState(false);
  const [showCloseModal, setShowCloseModal] = useState(false);
  const [showAnalysisChat, setShowAnalysisChat] = useState(false);
  const [showPlaybookModal, setShowPlaybookModal] = useState(false);

  // Marks the alert as being actively worked before it's closed, and claims
  // it for whoever clicked -- two independent calls (assignee, then status),
  // same non-atomic two-step pattern SeverityOverridePanel's own save()
  // already uses for a similar bundled update.
  async function startInvestigating() {
    if (!id || !user) return;
    setStartingInvestigation(true);
    setActionError(null);
    try {
      await api.put(`/api/v1/alerts/${id}/assignee`, { analystId: user.id }, token);
      await api.post(`/api/v1/alerts/${id}/status`, { status: "investigating" }, token);
      reload();
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    } finally {
      setStartingInvestigation(false);
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

  if (loading) return <AlertDetailSkeleton />;
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
            {current.duplicateCount > 0 && (
              <span className="badge badge-muted">{t("alerts.detail.duplicateNote", { count: current.duplicateCount })}</span>
            )}
          </div>
          <h1 className="page-title" style={{ marginBottom: 4 }}>
            {current.title}
          </h1>
          <p className="page-sub" style={{ marginBottom: 0 }}>
            {t("alerts.detail.receivedLine", { source: current.source, date: formatDateTime(current.receivedAt) })}
          </p>
        </div>
        <div className="toolbar-actions">
          <button className="btn btn-primary btn-sm" onClick={() => setShowAnalysisChat(true)}>
            <SparkleIcon width={14} height={14} />
            {current.latestAnalysisStatus === "running" || current.latestAnalysisStatus === "paused"
              ? t("alerts.detail.analyzing")
              : t("alerts.detail.analyzeWithAI")}
          </button>
          {current.status === "open" && (
            <button className="btn btn-sm" disabled={startingInvestigation} onClick={startInvestigating}>
              {startingInvestigation ? t("alerts.detail.startingInvestigation") : t("alerts.detail.startInvestigating")}
            </button>
          )}
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

      <AutoSaveTagPicker resourcePath={`/api/v1/alerts/${current.id}/tags`} value={current.tags} onSaved={reload} />

      {actionError && <div className="error-banner">{actionError}</div>}

      {showAnalysisChat && (
        <AnalysisChat contextType="alert" contextId={current.id} onClose={() => setShowAnalysisChat(false)} />
      )}

      {showPlaybookModal && current.playbookId && (
        <PlaybookViewModal playbookId={current.playbookId} alertId={current.id} onClose={() => setShowPlaybookModal(false)} />
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
            <AddNoteForm kind="alert" id={current.id} onAdded={reloadComments} />
          </div>
        </div>

        <div className="detail-side">
          {current.playbookId && (
            <div className="panel">
              <h2 className="panel-title" style={{ marginBottom: 10 }}>
                {t("alerts.detail.relatedPlaybookTitle")}
              </h2>
              <p style={{ margin: "0 0 12px", fontSize: 13, fontWeight: 600 }}>{current.playbookTitle}</p>
              <button
                className="btn btn-sm"
                style={{ width: "100%", justifyContent: "center" }}
                onClick={() => setShowPlaybookModal(true)}
              >
                {t("alerts.detail.viewPlaybook")}
              </button>
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

