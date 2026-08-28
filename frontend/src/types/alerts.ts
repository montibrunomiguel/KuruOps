// Mirrors backend/internal/domain/alert.go (json tags added there
// specifically so this file's casing matches the wire format).

export type Severity = "critical" | "high" | "medium" | "low" | "informational";
export type AlertStatus = "open" | "investigating" | "escalated" | "closed";
export type Classification = "false_positive" | "true_positive" | "authorized_event";

export interface Alert {
  id: string;
  tenantId: string;
  externalId?: string;
  webhookEndpointId?: string;
  title: string;
  source: string;
  severity: Severity;
  originalSeverity: Severity;
  status: AlertStatus;
  classification?: Classification;
  closeComment?: string;
  closeAttachmentUrl?: string;
  ruleId?: string;
  asset?: string;
  srcIp?: string;
  tags: string[];
  payload: unknown;
  // Sender-curated key/value list -- Slack channel, playbook link,
  // environment, or anything else the source wants surfaced (see backend
  // internal/ingest's extractMetadata). Always an object, possibly empty.
  metadata: Record<string, unknown>;
  incidentId?: string;
  // Playbook auto-assigned at ingest time (most specific alertNamePattern
  // match, or the tenant's default playbook) -- both undefined when neither
  // exists. playbookTitle is denormalized for display without a second
  // fetch; open the full playbook via playbookId when needed.
  playbookId?: string;
  playbookTitle?: string;
  assignedAnalystId?: string;
  assignedAnalystName?: string;
  receivedAt: string;
  acknowledgedAt?: string;
  closedAt?: string;
  createdAt: string;
  updatedAt: string;
  // Most recent "Analyze with AI" run -- resolved live at Get time (see
  // backend AlertService.Get / domain.Alert's fields of the same name).
  // Analysis runs in the background on the server (POST /analyze returns
  // 202 immediately): latestAnalysisStatus is undefined if none has ever
  // been requested, else "running" | "paused" | "completed" | "failed".
  // latestAnalysis (the result text) is only set once status is
  // "completed"; latestAnalysisError only once it's "failed".
  latestAnalysis?: string;
  latestAnalysisStatus?: "running" | "paused" | "completed" | "failed";
  latestAnalysisError?: string;
  // Count of subsequent alerts suppressed as duplicates of this one, via the
  // webhook endpoint's group-by-fields dedup window. 0 means never deduped.
  duplicateCount: number;
}

export type AlertEventType =
  | "received"
  | "status_changed"
  | "severity_overridden"
  | "closed"
  | "escalated"
  | "tags_changed"
  | "linked"
  | "ai_analysis_run"
  | "assignee_changed"
  | "duplicate_suppressed"
  | "playbook_webhook_triggered";

export interface AlertComment {
  id: string;
  alertId: string;
  tenantId: string;
  authorId: string;
  authorName: string;
  body: string;
  attachmentUrl?: string;
  createdAt: string;
}
