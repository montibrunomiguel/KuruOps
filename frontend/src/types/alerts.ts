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
  incidentId?: string;
  assignedAnalystId?: string;
  assignedAnalystName?: string;
  receivedAt: string;
  acknowledgedAt?: string;
  closedAt?: string;
  createdAt: string;
  updatedAt: string;
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
