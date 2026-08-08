import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { Alert, Severity } from "../../types/alerts";
import type {
  Incident,
  IncidentComment,
  IncidentEvent,
  IncidentPhase,
  IncidentPriority,
  IncidentRole,
  IncidentStatusHistoryEntry,
} from "../../types/incidents";
import { NIST_PHASE_ORDER, INCIDENT_ROLE_ORDER, SINGLE_ASSIGNEE_ROLES } from "../../types/incidents";
import type { UserSummary } from "../../types/users";
import { SeverityBadge, PriorityBadge, AlertStatusBadge } from "../../components/badges";
import { TagPicker } from "../../components/TagPicker";
import { AssigneePicker } from "../../components/AssigneePicker";
import { AttachmentButton, AttachmentPreview } from "../../components/AttachmentButton";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { SparkleIcon } from "../../components/icons";
import { formatDateTime, shortId } from "../../lib/format";
import { PRIORITY_ORDER } from "../../lib/chartColors";

const MATRIX_SEVERITIES: Severity[] = ["critical", "high", "medium", "low", "informational"];

export function IncidentDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const { token } = useAuth();
  const navigate = useNavigate();

  const { data: incidentData, loading, error, reload } = useList<Incident>(
    async (tk) => [await api.get<Incident>(`/api/v1/incidents/${id}`, tk)],
    [id],
  );
  const incident = incidentData?.[0];

  const { data: comments, reload: reloadComments } = useList<IncidentComment>(
    (tk) => api.get<IncidentComment[]>(`/api/v1/incidents/${id}/comments`, tk),
    [id],
  );
  const { data: linkedAlerts, reload: reloadLinkedAlerts } = useList<Alert>(
    (tk) => api.get<Alert[]>(`/api/v1/incidents/${id}/alerts`, tk),
    [id],
  );
  const { data: timeline, reload: reloadTimeline } = useList<IncidentEvent>(
    (tk) => api.get<IncidentEvent[]>(`/api/v1/incidents/${id}/timeline`, tk),
    [id],
  );
  const { data: statusHistory, reload: reloadStatusHistory } = useList<IncidentStatusHistoryEntry>(
    (tk) => api.get<IncidentStatusHistoryEntry[]>(`/api/v1/incidents/${id}/status-history`, tk),
    [id],
  );

  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [analyzing, setAnalyzing] = useState(false);
  const [aiResult, setAiResult] = useState<string | null>(null);

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

  async function analyze() {
    if (!id) return;
    setAnalyzing(true);
    setActionError(null);
    try {
      const res = await api.post<{ result: string }>(`/api/v1/incidents/${id}/analyze`, {}, token);
      setAiResult(res.result);
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    } finally {
      setAnalyzing(false);
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
          <button className="btn btn-primary btn-sm" disabled={analyzing} onClick={analyze}>
            <SparkleIcon width={14} height={14} />
            {analyzing ? t("incidents.detail.analyzing") : t("incidents.detail.analyzeWithAI")}
          </button>
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

      <IncidentTagsRow incident={incident} onSaved={reloadIncidentAndTimeline} />

      {actionError && <div className="error-banner">{actionError}</div>}

      {aiResult && (
        <div className="panel" style={{ marginBottom: 16, borderColor: "var(--accent)" }}>
          <h2 className="panel-title" style={{ marginBottom: 10 }}>
            {t("alerts.detail.aiResultTitle")}
          </h2>
          <p style={{ margin: 0, fontSize: 13, whiteSpace: "pre-wrap" }}>{aiResult}</p>
        </div>
      )}

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
            <LinkAlertForm
              incidentId={incident.id}
              onLinked={() => {
                reloadLinkedAlerts();
                reloadTimeline();
              }}
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
            <AddCommentForm incidentId={incident.id} onAdded={reloadComments} />
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

function initials(name?: string): string {
  if (!name) return "?";
  const parts = name.trim().split(/\s+/);
  const first = parts[0]?.[0] ?? "";
  const last = parts.length > 1 ? parts[parts.length - 1]?.[0] ?? "" : "";
  return (first + last).toUpperCase();
}

function IncidentTagsRow({ incident, onSaved }: { incident: Incident; onSaved: () => void }) {
  const [tags, setTags] = useState<string[]>(incident.tags);
  const [error, setError] = useState<string | null>(null);
  const { token } = useAuth();

  async function save(next: string[]) {
    setTags(next);
    setError(null);
    try {
      await api.put(`/api/v1/incidents/${incident.id}/tags`, { tags: next }, token);
      onSaved();
    } catch (err) {
      setTags(incident.tags);
      setError(mutationErrorMessage(err));
    }
  }

  return (
    <div style={{ marginBottom: 16 }}>
      <TagPicker value={tags} onChange={save} />
      {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
    </div>
  );
}

// IncidentRolesPanel is the NIST 800-61 "Team Roles" section -- the only
// per-person assignment UI on the incident detail page (the generic
// assignees panel that used to sit alongside it was removed; Commander/
// Technical Lead/etc. now fully replace it). Commander/Technical Lead
// render as a plain single-select (at most one person); the other three
// reuse AssigneePicker, same auto-save-on-change UX as IncidentTagsRow.
function IncidentRolesPanel({ incident, onSaved }: { incident: Incident; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: directory } = useList<UserSummary>((tk) => api.get<UserSummary[]>("/api/v1/users/directory", tk));
  const [error, setError] = useState<string | null>(null);
  const [submittingRole, setSubmittingRole] = useState<IncidentRole | null>(null);

  function usersFor(role: IncidentRole): string[] {
    return incident.roles.filter((r) => r.role === role).map((r) => r.user.id);
  }

  async function save(role: IncidentRole, userIds: string[]) {
    setSubmittingRole(role);
    setError(null);
    try {
      await api.put(`/api/v1/incidents/${incident.id}/roles/${role}`, { userIds }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmittingRole(null);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 12 }}>
        {t("incidents.roles.title")}
      </h2>
      {error && <div className="error-banner" style={{ marginBottom: 10 }}>{error}</div>}
      {INCIDENT_ROLE_ORDER.map((role) => {
        const isSingleAssignee = SINGLE_ASSIGNEE_ROLES.includes(role);
        const current = usersFor(role);
        const disabled = submittingRole === role;
        return (
          <div key={role} style={{ marginBottom: 14 }}>
            <label htmlFor={`incident-role-${role}`} style={{ display: "block", marginBottom: 4, fontWeight: 600, fontSize: 13 }}>
              {t(`incidents.roles.role.${role}`)}
            </label>
            {isSingleAssignee ? (
              <select
                id={`incident-role-${role}`}
                className="select"
                value={current[0] ?? ""}
                disabled={disabled}
                onChange={(e) => save(role, e.target.value ? [e.target.value] : [])}
              >
                <option value="">{t("incidents.roles.unassigned")}</option>
                {(directory ?? []).map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.name}
                  </option>
                ))}
              </select>
            ) : (
              <AssigneePicker value={current} onChange={(next) => save(role, next)} disabled={disabled} />
            )}
          </div>
        );
      })}
    </div>
  );
}

function NistMatrixPanel({ incident, onSaved }: { incident: Incident; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [submitting, setSubmitting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function apply(severity: Severity, priority: IncidentPriority) {
    if (incident.severity === severity && incident.priority === priority) return;
    const key = `${severity}-${priority}`;
    setSubmitting(key);
    setError(null);
    try {
      await api.post(`/api/v1/incidents/${incident.id}/severity-priority`, { severity, priority }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(null);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 4 }}>
        {t("incidents.detail.matrixTitle")}
      </h2>
      <p className="helper-text" style={{ marginBottom: 12 }}>
        {t("incidents.detail.matrixHint")}
      </p>
      {error && <div className="error-banner">{error}</div>}
      <div className="nist-matrix">
        <span />
        {PRIORITY_ORDER.map((p) => (
          <span className="nist-matrix-header-cell" key={p}>
            {p.toUpperCase()}
          </span>
        ))}
        {MATRIX_SEVERITIES.map((sev) => (
          <>
            <span className="nist-matrix-row-label" key={`label-${sev}`}>
              {t(`common.severity.${sev}`)}
            </span>
            {PRIORITY_ORDER.map((p) => {
              const active = incident.severity === sev && incident.priority === p;
              const key = `${sev}-${p}`;
              return (
                <button
                  type="button"
                  className="nist-matrix-cell"
                  key={key}
                  data-active={active}
                  disabled={submitting === key}
                  onClick={() => apply(sev, p)}
                  aria-label={`${t(`common.severity.${sev}`)} / ${p.toUpperCase()}`}
                >
                  {active && <span className="nist-matrix-cell-dot" />}
                </button>
              );
            })}
          </>
        ))}
      </div>
    </div>
  );
}

function DescriptionPanel({ incident, onSaved }: { incident: Incident; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(incident.description);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    setSubmitting(true);
    setError(null);
    try {
      await api.put(`/api/v1/incidents/${incident.id}/description`, { description: value }, token);
      setEditing(false);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("incidents.detail.descriptionTitle")}</h2>
        {!editing && (
          <button
            className="btn btn-ghost btn-sm"
            style={{ color: "var(--accent)" }}
            onClick={() => {
              setValue(incident.description);
              setEditing(true);
            }}
          >
            {t("common.edit")}
          </button>
        )}
      </div>
      {error && <div className="error-banner">{error}</div>}
      {editing ? (
        <>
          <textarea className="textarea" style={{ width: "100%", minHeight: 100 }} value={value} onChange={(e) => setValue(e.target.value)} />
          <div className="row-actions" style={{ marginTop: 10 }}>
            <button className="btn btn-primary btn-sm" disabled={submitting} onClick={save}>
              {submitting ? t("common.saving") : t("common.save")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setEditing(false)}>
              {t("common.cancel")}
            </button>
          </div>
        </>
      ) : (
        <p style={{ margin: 0, fontSize: 13, whiteSpace: "pre-wrap" }}>
          {incident.description || <span style={{ color: "var(--text-muted)" }}>{t("incidents.detail.descriptionEmpty")}</span>}
        </p>
      )}
    </div>
  );
}

// StatusHistoryPanel surfaces incident_status_history -- one row per NIST
// phase the incident has entered, per db/migrations/0005_incidents.up.sql.
// entered_at is never edited directly (audit/chain-of-custody requirement):
// a correction writes corrected_entered_at + who/why into the same row,
// alongside a status_timestamp_corrected event in the append-only
// incident_events log (see CorrectPhaseTimestamp in incident_service.go).
// The datetime-local input is always visible per row (no toggle button) --
// the reason field only appears once its value actually diverges from the
// entry's current effective time, same dirty-tracking principle as
// TagsEditPanel/SeverityPriorityPanel below.
function StatusHistoryPanel({
  incidentId,
  currentPhase,
  entries,
  onCorrected,
}: {
  incidentId: string;
  currentPhase: IncidentPhase;
  entries: IncidentStatusHistoryEntry[];
  onCorrected: () => void;
}) {
  const { t } = useTranslation();
  const byPhase = new Map(entries.map((e) => [e.phase, e]));

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 12 }}>
        {t("incidents.detail.statusHistoryTitle")}
      </h2>
      {NIST_PHASE_ORDER.filter((p) => byPhase.has(p)).map((phase) => (
        <StatusHistoryRow
          key={phase}
          incidentId={incidentId}
          phase={phase}
          entry={byPhase.get(phase)!}
          isCurrent={phase === currentPhase}
          onCorrected={onCorrected}
        />
      ))}
    </div>
  );
}

function toDatetimeLocal(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function StatusHistoryRow({
  incidentId,
  phase,
  entry,
  isCurrent,
  onCorrected,
}: {
  incidentId: string;
  phase: IncidentPhase;
  entry: IncidentStatusHistoryEntry;
  isCurrent: boolean;
  onCorrected: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const effective = entry.correctedEnteredAt ?? entry.enteredAt;
  const [value, setValue] = useState(() => toDatetimeLocal(effective));
  const [reason, setReason] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const dirty = value !== toDatetimeLocal(effective);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!reason.trim()) {
      setError(t("incidents.detail.reasonRequired"));
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await api.post(
        `/api/v1/incidents/${incidentId}/status-history/${phase}/correct`,
        { enteredAt: new Date(value).toISOString(), reason },
        token,
      );
      setReason("");
      onCorrected();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form className="row" style={{ display: "block" }} onSubmit={save}>
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 10, marginBottom: dirty ? 8 : 0 }}>
        <span style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12.5, fontWeight: 600 }}>
          <span className="legend-dot" style={{ background: isCurrent ? "var(--accent)" : "var(--success)" }} />
          {t(`common.phase.${phase}`)}
        </span>
        <input
          type="datetime-local"
          className="input"
          style={{ fontSize: 11.5, padding: "4px 8px", minWidth: 0 }}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          aria-label={`${t("incidents.detail.correctTime")} — ${t(`common.phase.${phase}`)}`}
        />
      </div>
      {entry.correctedEnteredAt && (
        <p className="row-sub" style={{ marginTop: 4 }}>
          {t("incidents.detail.originalWas", {
            date: formatDateTime(entry.enteredAt),
            by: entry.correctedBy ? shortId(entry.correctedBy) : "—",
          })}
        </p>
      )}
      {dirty && (
        <div style={{ marginTop: 8 }}>
          {error && <div className="error-banner">{error}</div>}
          <textarea
            className="textarea"
            style={{ width: "100%", minHeight: 50 }}
            placeholder={t("incidents.detail.correctionReasonPlaceholder")}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
          <div className="row-actions" style={{ marginTop: 8 }}>
            <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
              {submitting ? t("common.saving") : t("incidents.detail.saveCorrection")}
            </button>
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => {
                setValue(toDatetimeLocal(effective));
                setReason("");
                setError(null);
              }}
            >
              {t("common.cancel")}
            </button>
          </div>
        </div>
      )}
    </form>
  );
}

function AddCommentForm({ incidentId, onAdded }: { incidentId: string; onAdded: () => void }) {
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
      await api.post(`/api/v1/incidents/${incidentId}/comments`, { body, attachmentUrl }, token);
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
        placeholder={t("incidents.detail.addNotePlaceholder")}
        value={body}
        onChange={(e) => setBody(e.target.value)}
      />
      <AttachmentButton kind="incident" id={incidentId} value={attachmentUrl} onChange={setAttachmentUrl} disabled={submitting} />
      <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
        {submitting ? t("incidents.detail.posting") : t("incidents.detail.post")}
      </button>
    </form>
  );
}

function LinkAlertForm({ incidentId, onLinked }: { incidentId: string; onLinked: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [alertId, setAlertId] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!alertId.trim()) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.put(`/api/v1/incidents/${incidentId}/alerts/${alertId.trim()}`, {}, token);
      setAlertId("");
      onLinked();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="step-editor-row" style={{ marginBottom: 12 }}>
      {error && <div className="error-banner">{error}</div>}
      <input
        className="input"
        placeholder={t("incidents.detail.linkAlertPlaceholder")}
        value={alertId}
        onChange={(e) => setAlertId(e.target.value)}
      />
      <button type="submit" className="btn btn-sm" disabled={submitting}>
        {t("incidents.detail.link")}
      </button>
    </form>
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

  switch (ev.eventType) {
    case "created":
      return { text: t("dashboard.activity.incident.created", { id: shortId(ev.incidentId) }), warning: false };
    case "phase_changed":
      return { text: `${phaseLabel(data.from)} → ${phaseLabel(data.to)}`, warning: false };
    case "phase_skipped":
      return { text: `${phaseLabel(data.from)} → ${phaseLabel(data.to)}`, warning: true };
    case "severity_priority_changed":
      return { text: `${data.severity} / ${String(data.priority).toUpperCase()}`, warning: false };
    case "description_edited":
      return { text: t("incidents.detail.descriptionTitle"), warning: false };
    case "status_timestamp_corrected":
      return { text: `${phaseLabel(data.phase)}: ${data.reason ?? ""}`, warning: false };
    case "alert_linked":
      return { text: shortId(String(data.alertId ?? "")), warning: false };
    case "alert_unlinked":
      return { text: shortId(String(data.alertId ?? "")), warning: false };
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
