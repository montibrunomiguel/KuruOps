import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { usePagedList, mutationErrorMessage } from "../../api/hooks";
import { useEventStream } from "../../api/eventStream";
import { useDebouncedValue } from "../../hooks/useDebouncedValue";
import type { Severity } from "../../types/alerts";
import type { BulkResponse } from "../../types/api";
import type { Incident, IncidentPhase, IncidentPriority } from "../../types/incidents";
import { NIST_PHASE_ORDER } from "../../types/incidents";
import { SeverityBadge, PriorityBadge, PhasePill } from "../../components/badges";
import { TagPicker } from "../../components/TagPicker";
import { AssigneePicker } from "../../components/AssigneePicker";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { SeverityFilter } from "../../components/SeverityFilter";
import { TimeRangeFilter, timeRangeParams, EMPTY_TIME_RANGE, type TimeRangeValue } from "../../components/TimeRangeFilter";
import { Pagination } from "../../components/Pagination";
import { Modal } from "../../components/Modal";
import { formatDuration, shortId } from "../../lib/format";

type SlaFilter = "" | "breached" | "ok";

// Bulk phase-change deliberately excludes post_incident -- reaching it via
// ChangePhase alone never stamps closedAt (only the explicit Close action
// does, see IncidentService.Close's doc comment), and bulk-close was
// explicitly descoped: closing still requires the single-item Close flow
// on the incident detail page. IncidentService.BulkChangePhase itself
// rejects the whole request if asked for post_incident, this filter just
// keeps that option from ever being offered in the first place.
const BULK_PHASE_OPTIONS = NIST_PHASE_ORDER.filter((p) => p !== "post_incident");

export function IncidentsListPage() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const navigate = useNavigate();
  const [severity, setSeverity] = useState<Severity | "">("");
  const [priority, setPriority] = useState<IncidentPriority | "">("");
  const [phase, setPhase] = useState<IncidentPhase | "">("");
  const [sla, setSla] = useState<SlaFilter>("");
  const [q, setQ] = useState("");
  const debouncedQ = useDebouncedValue(q);
  // Trimmed -- see AlertsListPage's identical trimmedQ for why.
  const trimmedQ = debouncedQ.trim();
  const [showCreate, setShowCreate] = useState(false);
  const [timeRange, setTimeRange] = useState<TimeRangeValue>(EMPTY_TIME_RANGE);
  // Memoized so timeRangeParams' internal Date.now() (for preset ranges)
  // isn't recomputed on every render -- only when the picker's own value
  // actually changes, same reasoning as the Dashboard tab panels.
  const range = useMemo(() => timeRangeParams(timeRange), [timeRange]);

  const {
    items: incidents,
    total,
    page,
    pageSize,
    totalPages,
    loading,
    error,
    setPage,
    setPageSize,
    reload,
  } = usePagedList<Incident>(
    ["incidents", severity, priority, phase, sla, trimmedQ, range.since, range.until],
    (tk, limit, offset) => {
      const params = new URLSearchParams();
      if (severity) params.set("severity", severity);
      if (priority) params.set("priority", priority);
      if (phase) params.set("phase", phase);
      if (sla) params.set("sla", sla);
      if (trimmedQ) params.set("q", trimmedQ);
      if (range.since) params.set("since", range.since);
      if (range.until) params.set("until", range.until);
      params.set("limit", String(limit));
      params.set("offset", String(offset));
      return api.getPaged<Incident>(`/api/v1/incidents?${params.toString()}`, tk);
    },
  );

  // Row selection -- scoped to what's currently on screen, same reasoning
  // as AlertsListPage's selection state, see its comment.
  const [selected, setSelected] = useState<Set<string>>(new Set());
  useEffect(() => {
    setSelected(new Set());
  }, [severity, priority, phase, sla, trimmedQ, range.since, range.until, page, pageSize]);

  function toggleRow(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  const allOnPageSelected = incidents.length > 0 && incidents.every((i) => selected.has(i.id));
  const someOnPageSelected = incidents.some((i) => selected.has(i.id));
  const selectAllRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (selectAllRef.current) {
      selectAllRef.current.indeterminate = someOnPageSelected && !allOnPageSelected;
    }
  }, [someOnPageSelected, allOnPageSelected]);

  function toggleSelectAll() {
    if (allOnPageSelected) {
      setSelected(new Set());
    } else {
      setSelected(new Set(incidents.map((i) => i.id)));
    }
  }

  const [bulkPhase, setBulkPhase] = useState<IncidentPhase>(BULK_PHASE_OPTIONS[0]);
  const [applying, setApplying] = useState(false);
  const [bulkError, setBulkError] = useState<string | null>(null);
  const [bulkSummary, setBulkSummary] = useState<{ success: number; failed: number } | null>(null);

  async function applyBulkPhase() {
    setApplying(true);
    setBulkError(null);
    setBulkSummary(null);
    try {
      const resp = await api.post<BulkResponse>(
        "/api/v1/incidents/bulk/phase",
        { ids: Array.from(selected), phase: bulkPhase },
        token,
      );
      const success = resp.results.filter((r) => r.success).length;
      const failed = resp.results.length - success;
      setBulkSummary({ success, failed });
      setSelected(new Set());
      reload();
    } catch (err) {
      setBulkError(mutationErrorMessage(err));
    } finally {
      setApplying(false);
    }
  }

  // Live updates: another analyst (or the same one, in another tab)
  // creating/changing an incident re-fetches the first page from scratch --
  // same simplification AlertsListPage makes, see its comment.
  useEventStream((event) => {
    if (event.type === "incident") reload();
  });

  return (
    <div>
      <div className="toolbar">
        <div className="toolbar-title">
          <h1 className="page-title">{t("incidents.title")}</h1>
          <p className="page-sub" style={{ marginBottom: 0 }}>
            {t("incidents.subtitle")}
          </p>
        </div>
        <WebhookStatusIndicator />
      </div>

      {showCreate && (
        <CreateIncidentForm
          onCancel={() => setShowCreate(false)}
          onCreated={(id) => {
            setShowCreate(false);
            reload();
            navigate(`/incidents/${id}`);
          }}
        />
      )}

      <div className="filter-bar" style={{ justifyContent: "space-between" }}>
        <div style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
          <input
            className="input input-search"
            placeholder={t("incidents.searchPlaceholder")}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <SeverityFilter value={severity} onChange={setSeverity} />
          <select
            className="select"
            aria-label={t("dashboard.filters.priorityFilterLabel")}
            value={priority}
            onChange={(e) => setPriority(e.target.value as IncidentPriority | "")}
          >
            <option value="">{t("dashboard.filters.allPriorities")}</option>
            <option value="p1">P1</option>
            <option value="p2">P2</option>
            <option value="p3">P3</option>
            <option value="p4">P4</option>
          </select>
          <select
            className="select"
            aria-label={t("dashboard.filters.statusFilterLabel")}
            value={phase}
            onChange={(e) => setPhase(e.target.value as IncidentPhase | "")}
          >
            <option value="">{t("dashboard.filters.allStatuses")}</option>
            {NIST_PHASE_ORDER.map((p) => (
              <option key={p} value={p}>
                {t(`common.phase.${p}`)}
              </option>
            ))}
          </select>
          <select
            className="select"
            aria-label={t("dashboard.filters.slaFilterLabel")}
            value={sla}
            onChange={(e) => setSla(e.target.value as SlaFilter)}
          >
            <option value="">{t("dashboard.filters.slaAny")}</option>
            <option value="breached">{t("incidents.table.slaBreached")}</option>
            <option value="ok">{t("incidents.table.slaOk")}</option>
          </select>
          <TimeRangeFilter value={timeRange} onChange={setTimeRange} />
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
          {!loading && <span className="chart-card-sub">{t("incidents.count", { count: total })}</span>}
          <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
            + {t("incidents.newIncident")}
          </button>
        </div>
      </div>

      {selected.size > 0 && (
        <div className="bulk-toolbar">
          <span className="bulk-toolbar-count">{t("incidents.bulk.selected", { count: selected.size })}</span>
          <select
            className="select"
            aria-label={t("incidents.bulk.phaseLabel")}
            value={bulkPhase}
            onChange={(e) => setBulkPhase(e.target.value as IncidentPhase)}
            disabled={applying}
          >
            {BULK_PHASE_OPTIONS.map((p) => (
              <option key={p} value={p}>
                {t(`common.phase.${p}`)}
              </option>
            ))}
          </select>
          <button className="btn btn-primary btn-sm" onClick={() => void applyBulkPhase()} disabled={applying}>
            {applying ? t("incidents.bulk.applying") : t("incidents.bulk.apply")}
          </button>
          <button className="btn btn-ghost btn-sm" onClick={() => setSelected(new Set())} disabled={applying}>
            {t("incidents.bulk.clearSelection")}
          </button>
        </div>
      )}
      {bulkError && <div className="error-banner">{bulkError}</div>}
      {bulkSummary && (
        <div className="helper-text" style={{ marginBottom: 12 }}>
          {bulkSummary.failed > 0
            ? t("incidents.bulk.resultSummary", { success: bulkSummary.success, failed: bulkSummary.failed })
            : t("incidents.bulk.allSucceeded", { count: bulkSummary.success })}
        </div>
      )}

      <div className="panel">
        {error && <div className="error-banner">{error}</div>}
        {loading && <div className="empty-state">{t("common.loading")}</div>}
        {!loading && incidents.length === 0 && <div className="empty-state">{t("incidents.empty")}</div>}
        {!loading && incidents.length > 0 && (
          <>
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th className="table-select-cell">
                      <input
                        type="checkbox"
                        ref={selectAllRef}
                        checked={allOnPageSelected}
                        onChange={toggleSelectAll}
                        aria-label={t("incidents.bulk.selectAllAria")}
                      />
                    </th>
                    <th>{t("incidents.table.id")}</th>
                    <th>{t("incidents.table.title")}</th>
                    <th>{t("incidents.table.severity")}</th>
                    <th>{t("incidents.table.priority")}</th>
                    <th>{t("incidents.table.status")}</th>
                    <th>{t("incidents.table.assignees")}</th>
                    <th>{t("incidents.table.sla")}</th>
                  </tr>
                </thead>
                <tbody>
                  {incidents.map((i) => (
                    <tr key={i.id}>
                      <td className="table-select-cell">
                        <input
                          type="checkbox"
                          checked={selected.has(i.id)}
                          onChange={() => toggleRow(i.id)}
                          aria-label={t("incidents.bulk.selectRowAria", { id: shortId(i.id) })}
                        />
                      </td>
                      <td className="mono">
                        <Link to={`/incidents/${i.id}`} className="row-link-stretch" aria-label={i.title}>
                          {shortId(i.id)}
                        </Link>
                      </td>
                      <td className="table-title-cell">
                        {i.title}
                        {i.tags.length > 0 && (
                          <div className="tag-chip-list" style={{ marginTop: 4 }}>
                            {i.tags.map((tag) => (
                              <span className="tag-chip" key={tag}>
                                {tag}
                              </span>
                            ))}
                          </div>
                        )}
                      </td>
                      <td>
                        <SeverityBadge severity={i.severity} />
                      </td>
                      <td>
                        <PriorityBadge priority={i.priority} />
                      </td>
                      <td>
                        <PhasePill phase={i.phase} />
                      </td>
                      <td className="table-sub-cell">
                        {i.assignees.length > 0 ? i.assignees.map((a) => a.name).join(", ") : "—"}
                      </td>
                      <td>
                        <SlaCell incident={i} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination
              page={page}
              pageSize={pageSize}
              total={total}
              totalPages={totalPages}
              onPageChange={setPage}
              onPageSizeChange={setPageSize}
            />
          </>
        )}
      </div>
    </div>
  );
}

function SlaCell({ incident }: { incident: Incident }) {
  const { t } = useTranslation();
  if (incident.closedAt) return <span className="table-sub-cell">{t("incidents.table.slaClosed")}</span>;
  if (incident.slaBreached) {
    const elapsed = incident.slaDueAt ? formatDuration((Date.now() - new Date(incident.slaDueAt).getTime()) / 1000) : "";
    return <span className="age-text tone-critical">{t("incidents.table.slaBreachedAgo", { time: elapsed })}</span>;
  }
  if (incident.slaDueAt) {
    const remaining = formatDuration((new Date(incident.slaDueAt).getTime() - Date.now()) / 1000);
    return <span className="table-sub-cell">{t("incidents.table.slaRemaining", { time: remaining })}</span>;
  }
  return <span className="badge badge-muted">{t("incidents.table.slaOk")}</span>;
}

function CreateIncidentForm({
  onCancel,
  onCreated,
}: {
  onCancel: () => void;
  onCreated: (id: string) => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [severity, setSeverity] = useState<Severity>("medium");
  const [priority, setPriority] = useState<IncidentPriority>("p3");
  const [assigneeIds, setAssigneeIds] = useState<string[]>([]);
  const [tags, setTags] = useState<string[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const res = await api.post<Incident>(
        "/api/v1/incidents",
        { title, description, severity, priority, tags, assigneeIds },
        token,
      );
      onCreated(res.id);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal onClose={onCancel} as="form" onSubmit={handleSubmit}>
      <div className="panel-header">
        <h2 className="modal-title" style={{ marginBottom: 0 }}>
          {t("incidents.createForm.title")}
        </h2>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel} aria-label={t("common.close")}>
          ×
        </button>
      </div>
      {error && <div className="error-banner">{error}</div>}
      <div className="field">
        <label htmlFor="inc-title">{t("incidents.createForm.titleLabel")}</label>
        <input
          id="inc-title"
          className="input"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder={t("incidents.createForm.titlePlaceholder")}
          required
        />
      </div>
      <div className="form-grid">
        <div className="field">
          <label htmlFor="inc-severity">{t("incidents.createForm.severity")}</label>
          <select
            id="inc-severity"
            className="select"
            value={severity}
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
          <label htmlFor="inc-priority">{t("incidents.createForm.priority")}</label>
          <select
            id="inc-priority"
            className="select"
            value={priority}
            onChange={(e) => setPriority(e.target.value as IncidentPriority)}
          >
            <option value="p1">P1</option>
            <option value="p2">P2</option>
            <option value="p3">P3</option>
            <option value="p4">P4</option>
          </select>
        </div>
      </div>
      <div className="field">
        <label>{t("incidents.createForm.assignees")}</label>
        <AssigneePicker value={assigneeIds} onChange={setAssigneeIds} />
      </div>
      <div className="field">
        <label htmlFor="inc-desc">{t("incidents.createForm.description")}</label>
        <textarea
          id="inc-desc"
          className="textarea"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder={t("incidents.createForm.descriptionPlaceholder")}
        />
      </div>
      <div className="field">
        <label>{t("incidents.createForm.tags")}</label>
        <TagPicker value={tags} onChange={setTags} />
      </div>
      <button type="submit" className="btn btn-primary" style={{ width: "100%", justifyContent: "center" }} disabled={submitting}>
        {submitting ? t("incidents.createForm.submitting") : t("incidents.createForm.submit")}
      </button>
    </Modal>
  );
}
