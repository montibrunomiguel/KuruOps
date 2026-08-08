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
  closeImageUrl?: string;
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
  assignedAnalystId?: string;
  assignedAnalystName?: string;
  receivedAt: string;
  acknowledgedAt?: string;
  closedAt?: string;
  createdAt: string;
  updatedAt: string;
  // Most recent completed "Analyze with AI" result -- resolved live at Get
  // time (see backend AlertService.Get / domain.Alert.LatestAnalysis),
  // undefined if no analysis has completed yet for this alert.
  latestAnalysis?: string;
}

export type AlertEventType =
  | "received"
  | "status_changed"
  | "severity_overridden"
  | "closed"
  | "escalated"
  | "tags_changed"
  | "linked"
  | "ai_analysis_run";

export interface AlertEvent {
  id: number;
  alertId: string;
  tenantId: string;
  eventType: AlertEventType;
  actorType: "user" | "system" | "ai";
  actorId?: string;
  data: unknown;
  createdAt: string;
}

export interface AlertComment {
  id: string;
  alertId: string;
  tenantId: string;
  authorId: string;
  authorName: string;
  body: string;
  imageUrl?: string;
  createdAt: string;
}
