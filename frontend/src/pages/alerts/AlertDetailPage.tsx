import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { Alert, AlertComment, AlertStatus, Classification, Severity } from "../../types/alerts";
import type { Playbook } from "../../types/playbooks";
import type { UserSummary } from "../../types/users";
import { SeverityBadge, AlertStatusBadge, ClassificationBadge } from "../../components/badges";
import { TagPicker } from "../../components/TagPicker";
import { ImageAttachButton } from "../../components/ImageAttachButton";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { SparkleIcon } from "../../components/icons";
import { formatDateTime, shortId } from "../../lib/format";

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
  const [aiResult, setAiResult] = useState<string | null>(null);
  const [analyzing, setAnalyzing] = useState(false);
  const [escalating, setEscalating] = useState(false);

  async function analyze() {
    if (!id) return;
    setAnalyzing(true);
    setActionError(null);
    try {
      const res = await api.post<{ result: string }>(`/api/v1/alerts/${id}/analyze`, {}, token);
      setAiResult(res.result);
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    } finally {
      setAnalyzing(false);
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
          <button className="btn btn-primary btn-sm" disabled={analyzing} onClick={analyze}>
            <SparkleIcon width={14} height={14} />
            {analyzing ? t("alerts.detail.analyzing") : t("alerts.detail.analyzeWithAI")}
          </button>
          {current.status !== "escalated" && current.status !== "closed" && (
            <button className="btn btn-danger btn-sm" disabled={escalating} onClick={escalate}>
              {escalating ? t("alerts.detail.escalating") : t("alerts.detail.escalateToIncident")}
            </button>
          )}
        </div>
      </div>

      <AlertTagsRow current={current} onSaved={reload} />

      {actionError && <div className="error-banner">{actionError}</div>}

      {(aiResult ?? current.latestAnalysis) && (
        <div className="panel" style={{ marginBottom: 16, borderColor: "var(--accent)" }}>
          <h2 className="panel-title" style={{ marginBottom: 10 }}>
            {t("alerts.detail.aiResultTitle")}
          </h2>
          <p style={{ margin: 0, fontSize: 13, whiteSpace: "pre-wrap" }}>{aiResult ?? current.latestAnalysis}</p>
        </div>
      )}

      <div className="detail-layout">
        <div className="detail-main">
          <MetadataPanel metadata={current.metadata} />
          <PayloadPanel payload={current.payload} />
          <ClassificationPanel alert={current} onSaved={reload} />
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

function initials(name?: string): string {
  if (!name) return "?";
  const parts = name.trim().split(/\s+/);
  const first = parts[0]?.[0] ?? "";
  const last = parts.length > 1 ? parts[parts.length - 1]?.[0] ?? "" : "";
  return (first + last).toUpperCase();
}

function AddAlertCommentForm({ alertId, onAdded }: { alertId: string; onAdded: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [body, setBody] = useState("");
  const [imageUrl, setImageUrl] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!body.trim()) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.post(`/api/v1/alerts/${alertId}/comments`, { body, imageUrl }, token);
      setBody("");
      setImageUrl(null);
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
      <ImageAttachButton kind="alert" id={alertId} value={imageUrl} onChange={setImageUrl} disabled={submitting} />
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

// MetadataPanel is the sender's own curated key/value list (Slack channel,
// playbook/runbook link, environment, anything else they chose to send --
// see backend domain.Alert.Metadata) -- unlike PayloadPanel below, this is
// not collapsed by default: it's specifically the subset of information
// the source considered important enough to call out. Renders nothing at
// all when metadata is empty, rather than an empty panel shell.
function MetadataPanel({ metadata }: { metadata: Record<string, unknown> | undefined }) {
  const { t } = useTranslation();
  const entries = Object.entries(metadata ?? {});
  if (entries.length === 0) return null;

  return (
    <div className="panel" style={{ marginBottom: 16 }}>
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.customMetadataTitle")}
      </h2>
      <div className="breakdown-list">
        {entries.map(([key, value]) => {
          const text = typeof value === "string" ? value : JSON.stringify(value);
          const isLink = typeof value === "string" && /^https?:\/\//i.test(value);
          return (
            <div key={key} className="legend-row">
              <span className="legend-row-label" style={{ fontWeight: 600 }}>
                {key}
              </span>
              <span className="legend-row-value" style={{ wordBreak: "break-word", textAlign: "right" }}>
                {isLink ? (
                  <a href={value as string} target="_blank" rel="noopener noreferrer">
                    {text}
                  </a>
                ) : (
                  text
                )}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function PayloadPanel({ payload }: { payload: unknown }) {
  const { t } = useTranslation();
  // Collapsed by default -- the raw webhook payload is the least-read panel
  // during triage, but as the first panel on the page it was pushing the
  // actually-actionable ones (classification, linked alerts) below the fold.
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("alerts.detail.payloadTitle")}</h2>
        <button className="btn btn-ghost btn-sm" style={{ color: "var(--accent)" }} onClick={() => setExpanded((v) => !v)}>
          {expanded ? t("alerts.detail.collapse") : t("alerts.detail.expand")}
        </button>
      </div>
      {expanded && (
        <pre
          style={{
            margin: 0,
            fontSize: 11.5,
            fontFamily: "IBM Plex Mono, monospace",
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
            color: "var(--text-secondary)",
          }}
        >
          {JSON.stringify(payload, null, 2)}
        </pre>
      )}
    </div>
  );
}

function ClassificationPanel({ alert, onSaved }: { alert: Alert; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [open, setOpen] = useState(false);
  const [classification, setClassification] = useState<Classification>("true_positive");
  const [comment, setComment] = useState("");
  const [imageUrl, setImageUrl] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await api.post(`/api/v1/alerts/${alert.id}/close`, { classification, comment, imageUrl }, token);
      setOpen(false);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.classificationTitle")}
      </h2>

      {alert.status === "closed" && alert.classification ? (
        <>
          <ClassificationBadge classification={alert.classification} />
          {alert.closeComment && (
            <p style={{ margin: "10px 0 0", fontSize: 13, color: "var(--text-secondary)" }}>{alert.closeComment}</p>
          )}
        </>
      ) : !open ? (
        <>
          <p className="helper-text" style={{ marginBottom: 12 }}>
            {t("alerts.detail.onlyOnClose")}
          </p>
          <button className="btn btn-primary btn-sm" style={{ width: "100%" }} onClick={() => setOpen(true)}>
            {t("alerts.detail.closeAndClassify")}
          </button>
        </>
      ) : (
        <form onSubmit={handleSubmit}>
          {error && <div className="error-banner">{error}</div>}
          <div style={{ display: "flex", flexDirection: "column", gap: 8, marginBottom: 12 }}>
            {(["false_positive", "true_positive", "authorized_event"] as Classification[]).map((c) => (
              <label
                key={c}
                style={{
                  display: "flex",
                  alignItems: "flex-start",
                  gap: 10,
                  padding: "8px 10px",
                  borderRadius: 7,
                  border: "1px solid var(--border-strong)",
                  cursor: "pointer",
                }}
              >
                <input
                  type="radio"
                  name="classification"
                  checked={classification === c}
                  onChange={() => setClassification(c)}
                  style={{ marginTop: 3 }}
                />
                <span>
                  <span style={{ display: "block", fontWeight: 600, fontSize: 12.5 }}>{t(`common.classification.${c}`)}</span>
                  <span style={{ display: "block", fontSize: 11, color: "var(--text-muted)" }}>
                    {t(`common.classificationHint.${c}`)}
                  </span>
                </span>
              </label>
            ))}
          </div>
          <textarea
            className="textarea"
            style={{ width: "100%", marginBottom: 10 }}
            placeholder={t("alerts.detail.closingComment")}
            value={comment}
            onChange={(e) => setComment(e.target.value)}
          />
          <div className="row-actions" style={{ marginBottom: 10 }}>
            <ImageAttachButton kind="alert" id={alert.id} value={imageUrl} onChange={setImageUrl} disabled={submitting} />
          </div>
          <div className="row-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={submitting} style={{ flex: 1, justifyContent: "center" }}>
              {submitting ? t("common.saving") : t("alerts.detail.confirmClose")}
            </button>
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </button>
          </div>
        </form>
      )}
    </div>
  );
}

function LinkedAlertsPanel({ alertId }: { alertId: string }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: linked, reload } = useList<Alert>((tk) => api.get<Alert[]>(`/api/v1/alerts/${alertId}/alerts`, tk), [alertId]);
  const { data: candidates } = useList<Alert>((tk) => api.get<Alert[]>(`/api/v1/alerts?limit=50`, tk));
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);

  const results = (Array.isArray(candidates) ? candidates : []).filter(
    (a) =>
      a.id !== alertId &&
      !(linked ?? []).some((l) => l.id === a.id) &&
      query.length > 0 &&
      (a.id.toLowerCase().includes(query.toLowerCase()) || a.title.toLowerCase().includes(query.toLowerCase())),
  );

  async function link(otherId: string) {
    setError(null);
    try {
      await api.put(`/api/v1/alerts/${alertId}/alerts/${otherId}`, {}, token);
      setQuery("");
      reload();
    } catch (err) {
      setError(mutationErrorMessage(err));
    }
  }

  async function unlink(otherId: string) {
    setError(null);
    try {
      await api.del(`/api/v1/alerts/${alertId}/alerts/${otherId}`, token);
      reload();
    } catch (err) {
      setError(mutationErrorMessage(err));
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.linkedAlertsTitle")}
      </h2>
      {error && <div className="error-banner">{error}</div>}
      {linked && linked.length === 0 && <div className="empty-state">{t("alerts.detail.noLinkedAlerts")}</div>}
      {linked && linked.length > 0 && (
        <div style={{ marginBottom: 10 }}>
          {linked.map((a) => (
            <span className="linked-chip" key={a.id}>
              <span className="mono">{shortId(a.id)}</span> · {a.title}
              <button type="button" onClick={() => unlink(a.id)} aria-label="unlink">
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      <input
        className="input"
        style={{ width: "100%" }}
        placeholder={t("alerts.detail.linkSearchPlaceholder")}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />
      {results.length > 0 && (
        <div className="search-result-list">
          {results.map((a) => (
            <div className="search-result-item" key={a.id} onClick={() => link(a.id)}>
              <span className="mono">{shortId(a.id)}</span> · {a.title}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function AssigneePanel({ alert, onSaved }: { alert: Alert; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: directory } = useList<UserSummary>((tk) => api.get<UserSummary[]>("/api/v1/users/directory", tk));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save(analystId: string) {
    setSubmitting(true);
    setError(null);
    try {
      await api.put(`/api/v1/alerts/${alert.id}/assignee`, { analystId: analystId || null }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.assigneeTitle")}
      </h2>
      {error && <div className="error-banner">{error}</div>}
      <select
        className="select"
        style={{ width: "100%" }}
        value={alert.assignedAnalystId ?? ""}
        disabled={submitting}
        onChange={(e) => save(e.target.value)}
        aria-label={t("alerts.detail.assigneeTitle")}
      >
        <option value="">{t("common.unassigned")}</option>
        {(directory ?? []).map((u) => (
          <option key={u.id} value={u.id}>
            {u.name}
          </option>
        ))}
      </select>
    </div>
  );
}

function SeverityOverridePanel({ alert, onSaved }: { alert: Alert; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [severity, setSeverity] = useState<Severity>(alert.severity);
  const [status, setStatus] = useState<AlertStatus>(alert.status === "closed" ? "open" : alert.status);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const readOnly = alert.status === "closed";
  const dirty = !readOnly && (severity !== alert.severity || status !== alert.status);

  async function save() {
    setSubmitting(true);
    setError(null);
    try {
      if (severity !== alert.severity) {
        await api.put(`/api/v1/alerts/${alert.id}/severity`, { severity }, token);
      }
      if (status !== alert.status) {
        await api.post(`/api/v1/alerts/${alert.id}/status`, { status }, token);
      }
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.severityOverrideTitle")}
      </h2>
      {error && <div className="error-banner">{error}</div>}
      <p className="helper-text" style={{ marginBottom: 10 }}>
        {t("alerts.detail.originalSeverity", { severity: t(`common.severity.${alert.originalSeverity}`) })}
      </p>
      <div className="field">
        <select
          className="select"
          style={{ width: "100%" }}
          value={severity}
          disabled={readOnly}
          onChange={(e) => setSeverity(e.target.value as Severity)}
        >
          <option value="critical">{t("common.severity.critical")}</option>
          <option value="high">{t("common.severity.high")}</option>
          <option value="medium">{t("common.severity.medium")}</option>
          <option value="low">{t("common.severity.low")}</option>
          <option value="informational">{t("common.severity.informational")}</option>
        </select>
      </div>
      <div className="field">
        <label htmlFor="ov-status">{t("alerts.detail.statusLabel")}</label>
        <select
          id="ov-status"
          className="select"
          value={status}
          disabled={readOnly}
          onChange={(e) => setStatus(e.target.value as AlertStatus)}
        >
          <option value="open">{t("common.alertStatus.open")}</option>
          <option value="investigating">{t("common.alertStatus.investigating")}</option>
          <option value="escalated">{t("common.alertStatus.escalated")}</option>
        </select>
      </div>
      {dirty && (
        <button className="btn btn-primary btn-sm" style={{ width: "100%" }} disabled={submitting} onClick={save}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
      )}
    </div>
  );
}
