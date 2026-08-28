import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import type { Alert } from "../../types/alerts";
import type { Incident, IncidentComment, IncidentEvent, IncidentPhase, IncidentStatusHistoryEntry } from "../../types/incidents";
import { NIST_PHASE_ORDER } from "../../types/incidents";
import { SeverityBadge, PriorityBadge, AlertStatusBadge } from "../../components/badges";
import { AutoSaveTagPicker } from "../../components/AutoSaveTagPicker";
import { AttachmentPreview } from "../../components/AttachmentButton";
import { AddNoteForm } from "../../components/AddNoteForm";
import { LinkSearchPicker } from "../../components/LinkSearchPicker";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { AnalysisChat } from "../../components/AnalysisChat";
import { IOCsModal } from "../../components/IOCsModal";
import { SparkleIcon } from "../../components/icons";
import { formatDateTime, initials, shortId } from "../../lib/format";
import { IncidentRolesPanel } from "./IncidentDetailPage/IncidentRolesPanel";
import { NistMatrixPanel } from "./IncidentDetailPage/NistMatrixPanel";
import { DescriptionPanel } from "./IncidentDetailPage/DescriptionPanel";
import { StatusHistoryPanel } from "./IncidentDetailPage/StatusHistoryPanel";

export function IncidentDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const { token } = useAuth();
  const navigate = useNavigate();

  const { data: incidentData, loading, error, reload } = useList<Incident>(
    ["incident", id],
    async (tk) => [await api.get<Incident>(`/api/v1/incidents/${id}`, tk)],
  );
  const incident = incidentData?.[0];

  const { data: comments, reload: reloadComments } = useList<IncidentComment>(
    ["incident-comments", id],
    (tk) => api.get<IncidentComment[]>(`/api/v1/incidents/${id}/comments`, tk),
  );
  const { data: linkedAlerts, reload: reloadLinkedAlerts } = useList<Alert>(
    ["incident-linked-alerts", id],
    (tk) => api.get<Alert[]>(`/api/v1/incidents/${id}/alerts`, tk),
  );
  const { data: alertCandidates } = useList<Alert>(["incident-alert-candidates"], (tk) => api.get<Alert[]>(`/api/v1/alerts?limit=50`, tk));
  const { data: timeline, reload: reloadTimeline } = useList<IncidentEvent>(
    ["incident-timeline", id],
    (tk) => api.get<IncidentEvent[]>(`/api/v1/incidents/${id}/timeline`, tk),
  );
  const { data: statusHistory, reload: reloadStatusHistory } = useList<IncidentStatusHistoryEntry>(
    ["incident-status-history", id],
    (tk) => api.get<IncidentStatusHistoryEntry[]>(`/api/v1/incidents/${id}/status-history`, tk),
  );

  // Analysis runs in the background on the server (POST /analyze returns
  // 202 immediately, see AIAnalysisService's doc comment) -- this is what
  // tells the page to refetch once it's done, instead of waiting on the
  // POST response the way it used to. Filtered to this incident's id:
  // other incidents changing shouldn't reload this page. Also refreshes
  // the timeline, since a finished analysis appends an ai_analysis_run
  // entry to it.
  useEventStream((event) => {
    if (event.type !== "incident") return;
    const payload = event.data as { id?: string } | null;
    if (payload?.id !== id) return;
    reload();
    reloadTimeline();
  });

  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [generatingPostmortem, setGeneratingPostmortem] = useState(false);
  const [generatingReport, setGeneratingReport] = useState(false);
  const [showAnalysisChat, setShowAnalysisChat] = useState(false);
  const [showIOCs, setShowIOCs] = useState(false);

  // Every mutation below can append a row to the incident_events timeline
  // (phase_changed, closed, severity_priority_changed, description_edited,
  // alert_linked/unlinked -- see domain.IncidentEventType), so each one
  // reloads the timeline alongside whatever state it directly touches.
  function reloadIncidentAndTimeline() {
    reload();
    reloadTimeline();
  }

  async function changePhase(phase: IncidentPhase) {
    if (!id) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(`/api/v1/incidents/${id}/phase`, { phase }, token);
      reloadIncidentAndTimeline();
      reloadStatusHistory();
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function closeIncident() {
    if (!id) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(`/api/v1/incidents/${id}/close`, {}, token);
      reloadIncidentAndTimeline();
      reloadStatusHistory();
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function linkAlert(alertId: string) {
    if (!id) return;
    setActionError(null);
    try {
      await api.put(`/api/v1/incidents/${id}/alerts/${alertId}`, {}, token);
      reloadLinkedAlerts();
      reloadTimeline();
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    }
  }

  async function unlinkAlert(alertId: string) {
    if (!id) return;
    setActionError(null);
    try {
      await api.del(`/api/v1/incidents/${id}/alerts/${alertId}`, token);
      reloadLinkedAlerts();
      reloadTimeline();
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    }
  }

  async function downloadPostmortem() {
    if (!id) return;
    setGeneratingPostmortem(true);
    setActionError(null);
    try {
      const { blob, filename } = await api.downloadFile(`/api/v1/incidents/${id}/postmortem`, token);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    } finally {
      setGeneratingPostmortem(false);
    }
  }

  // Unlike the postmortem (only offered once the incident has actually
  // reached post_incident -- see the button's own gate below), the PDF
  // report is available at any phase: it's a point-in-time factual export
  // (current state + history so far), not a closing narrative document, so
  // there's no phase it wouldn't make sense to pull one from.
  async function downloadReport() {
    if (!id) return;
    setGeneratingReport(true);
    setActionError(null);
    try {
      const { blob, filename } = await api.downloadFile(`/api/v1/incidents/${id}/report.pdf`, token);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    } finally {
      setGeneratingReport(false);
    }
  }

  if (loading) return <div className="empty-state">{t("common.loading")}</div>;
  if (error) return <div className="error-banner">{error}</div>;
  if (!incident) return <div className="empty-state">{t("incidents.detail.notFound")}</div>;

  return (
    <div>
      <div className="toolbar">
        <Link to="/incidents" className="back-link" style={{ marginBottom: 0 }}>
          ‹ {t("incidents.detail.backToIncidents")}
        </Link>
        <WebhookStatusIndicator />
      </div>

      <div className="detail-header">
        <div>
          <div className="detail-header-badges" style={{ marginTop: 0, marginBottom: 8 }}>
            <span className="mono" style={{ color: "var(--text-muted)", fontSize: 12.5 }}>
              #{shortId(incident.id)}
            </span>
            <SeverityBadge severity={incident.severity} />
            <PriorityBadge priority={incident.priority} />
            {incident.slaBreached && <span className="badge badge-critical">{t("incidents.detail.slaBreachedBadge")}</span>}
            {incident.closedAt && <span className="badge badge-muted">{t("incidents.detail.closedBadge")}</span>}
          </div>
          <h1 className="page-title" style={{ marginBottom: 4 }}>
            {incident.title}
          </h1>
          <p className="page-sub" style={{ marginBottom: 0 }}>
            {t("incidents.detail.openedLine", { date: formatDateTime(incident.openedAt) })}
          </p>
        </div>
        <div className="toolbar-actions">
          <button className="btn btn-primary btn-sm" onClick={() => setShowAnalysisChat(true)}>
            <SparkleIcon width={14} height={14} />
            {incident.latestAnalysisStatus === "running" || incident.latestAnalysisStatus === "paused"
              ? t("incidents.detail.analyzing")
              : t("incidents.detail.analyzeWithAI")}
          </button>
          <button className="btn btn-sm" onClick={() => setShowIOCs(true)}>
            {t("incidents.iocs.button")}
          </button>
          <button className="btn btn-sm" disabled={generatingReport} onClick={downloadReport}>
            {generatingReport ? t("incidents.detail.generatingReport") : t("incidents.detail.downloadReport")}
          </button>
          {incident.phase === "post_incident" && (
            <button className="btn btn-sm" disabled={generatingPostmortem} onClick={downloadPostmortem}>
              {generatingPostmortem ? t("incidents.detail.generatingPostmortem") : t("incidents.detail.generatePostmortem")}
            </button>
          )}
          {!incident.closedAt && (
            <button
              className="btn btn-sm"
              disabled={busy || incident.phase !== "post_incident"}
              onClick={closeIncident}
              title={incident.phase !== "post_incident" ? t("incidents.detail.closeRequiresPostIncident") : undefined}
            >
              {busy ? t("incidents.detail.closing") : t("incidents.detail.closeIncident")}
            </button>
          )}
        </div>
      </div>

      <AutoSaveTagPicker
        resourcePath={`/api/v1/incidents/${incident.id}/tags`}
        value={incident.tags}
        onSaved={reloadIncidentAndTimeline}
      />

      {actionError && <div className="error-banner">{actionError}</div>}

      {showAnalysisChat && (
        <AnalysisChat contextType="incident" contextId={incident.id} onClose={() => setShowAnalysisChat(false)} />
      )}

      {showIOCs && <IOCsModal incidentId={incident.id} onClose={() => setShowIOCs(false)} />}

      <div className="panel" style={{ marginBottom: 16 }}>
        <div className="phase-tracker">
          {NIST_PHASE_ORDER.map((p, idx) => (
            <button
              key={p}
              className="phase-step"
              disabled={busy || !!incident.closedAt}
              data-current={p === incident.phase}
              data-done={idx < NIST_PHASE_ORDER.indexOf(incident.phase)}
              onClick={() => changePhase(p)}
            >
              {t(`common.phase.${p}`)}
            </button>
          ))}
        </div>
      </div>

      <div className="detail-layout">
        <div className="detail-main">
          <DescriptionPanel incident={incident} onSaved={reloadIncidentAndTimeline} />

          <div className="panel">
            <h2 className="panel-title" style={{ marginBottom: 10 }}>
              {t("incidents.detail.timelineTitle")}
            </h2>
            {timeline && timeline.length === 0 && <div className="empty-state">{t("incidents.detail.timelineEmpty")}</div>}
            {timeline && timeline.length > 0 && (
              <div className="timeline">
                {timeline.map((ev) => {
                  const { text, warning } = describeIncidentEvent(ev, t);
                  return (
                    <div className="timeline-item" key={ev.id}>
                      <span className={warning ? "timeline-dot timeline-dot-warning" : "timeline-dot"} />
                      <div className="timeline-body">
                        <p className="timeline-text">
                          {text}
                          {warning && (
                            <span className="badge badge-sev-high" style={{ marginLeft: 6 }}>
                              {t("incidents.detail.warningBadge")}
                            </span>
                          )}
                        </p>
                        <span className="timeline-time">{formatDateTime(ev.createdAt)}</span>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>

          <div className="panel">
            <h2 className="panel-title" style={{ marginBottom: 10 }}>
              {t("incidents.detail.correlatedAlertsTitle")}
            </h2>
            <LinkSearchPicker
              candidates={alertCandidates ?? []}
              excludeIds={new Set((linkedAlerts ?? []).map((a) => a.id))}
              onLink={linkAlert}
              placeholder={t("incidents.detail.linkAlertPlaceholder")}
            />
            {linkedAlerts && linkedAlerts.length === 0 && (
              <div className="empty-state">{t("incidents.detail.noCorrelatedAlerts")}</div>
            )}
            {linkedAlerts &&
              linkedAlerts.map((a) => (
                <div className="row" key={a.id}>
                  <div className="row-main" style={{ cursor: "pointer", display: "flex", alignItems: "center", gap: 10 }} onClick={() => navigate(`/alerts/${a.id}`)}>
                    <SeverityBadge severity={a.severity} />
                    <span className="mono" style={{ color: "var(--text-muted)", fontSize: 12 }}>
                      {shortId(a.id)}
                    </span>
                    <p className="row-title" style={{ margin: 0 }}>
                      {a.title}
                    </p>
                  </div>
                  <div className="row-actions">
                    <AlertStatusBadge status={a.status} />
                    <button className="btn btn-ghost btn-sm" onClick={() => unlinkAlert(a.id)}>
                      {t("incidents.detail.unlink")}
                    </button>
                  </div>
                </div>
              ))}
          </div>

          <div className="panel">
            <h2 className="panel-title" style={{ marginBottom: 10 }}>
              {t("incidents.detail.teamNotesTitle")}
            </h2>
            {comments && comments.length === 0 && <div className="empty-state">{t("incidents.detail.noNotes")}</div>}
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
            <AddNoteForm kind="incident" id={incident.id} onAdded={reloadComments} />
          </div>
        </div>

        <div className="detail-side">
          <IncidentRolesPanel incident={incident} onSaved={reloadIncidentAndTimeline} />
          <NistMatrixPanel incident={incident} onSaved={reloadIncidentAndTimeline} />
          <StatusHistoryPanel
            incidentId={incident.id}
            currentPhase={incident.phase}
            entries={statusHistory ?? []}
            onCorrected={() => {
              reloadStatusHistory();
              reloadTimeline();
            }}
          />
        </div>
      </div>
    </div>
  );
}

// describeIncidentEvent turns a raw IncidentEvent into a human-readable
// timeline line. phase_skipped gets `warning: true` -- the backend logs it
// (ChangePhase in incident_service.go) precisely so a jump over one or more
// NIST phases is visible instead of silently blending into MTTR, per the
// "don't mask a badly-followed process" requirement -- so the frontend
// should make it visually stand out too, not just render it like any other
// event type.
function describeIncidentEvent(ev: IncidentEvent, t: (key: string, opts?: Record<string, unknown>) => string): { text: string; warning: boolean } {
  const data = (ev.data ?? {}) as Record<string, unknown>;
  const phaseLabel = (v: unknown) => (typeof v === "string" ? t(`common.phase.${v}`) : String(v));
  const severityLabel = (v: unknown) => (typeof v === "string" ? t(`common.severity.${v}`) : String(v));

  switch (ev.eventType) {
    case "created":
      return { text: t("dashboard.activity.incident.created", { id: shortId(ev.incidentId) }), warning: false };
    case "phase_changed":
      return { text: `${phaseLabel(data.from)} → ${phaseLabel(data.to)}`, warning: false };
    case "phase_skipped":
      return { text: `${phaseLabel(data.from)} → ${phaseLabel(data.to)}`, warning: true };
    case "severity_priority_changed":
      return { text: `${severityLabel(data.severity)} / ${String(data.priority).toUpperCase()}`, warning: false };
    case "description_edited":
      return { text: t("incidents.detail.descriptionTitle"), warning: false };
    case "status_timestamp_corrected":
      return { text: `${phaseLabel(data.phase)}: ${data.reason ?? ""}`, warning: false };
    case "alert_linked":
      return {
        text: t("dashboard.activity.incident.alert_linked", { id: shortId(ev.incidentId), alertId: shortId(String(data.alertId ?? "")) }),
        warning: false,
      };
    case "alert_unlinked":
      return {
        text: t("dashboard.activity.incident.alert_unlinked", { id: shortId(ev.incidentId), alertId: shortId(String(data.alertId ?? "")) }),
        warning: false,
      };
    case "ai_analysis_run":
      return { text: t("incidents.detail.analyzeWithAI"), warning: false };
    case "closed":
      return { text: t("incidents.detail.closedEvent"), warning: false };
    case "assignees_changed": {
      const names = Array.isArray(data.assignees) ? (data.assignees as unknown[]).map(String) : [];
      return { text: names.length > 0 ? names.join(", ") : t("common.unassigned"), warning: false };
    }
    case "role_assigned":
    case "role_unassigned": {
      const names = Array.isArray(data.assignees) ? (data.assignees as unknown[]).map(String) : [];
      const roleLabel = typeof data.role === "string" ? t(`incidents.roles.role.${data.role}`) : String(data.role);
      return { text: `${roleLabel}: ${names.length > 0 ? names.join(", ") : t("common.unassigned")}`, warning: false };
    }
    default:
      return { text: ev.eventType.replace(/_/g, " "), warning: false };
  }
}
