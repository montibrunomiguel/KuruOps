// Mirrors backend/internal/domain/incident.go.
import type { Severity } from "./alerts";
import type { UserSummary } from "./users";

export type IncidentPhase =
  | "new"
  | "detection_analysis"
  | "containment"
  | "eradication"
  | "recovery"
  | "post_incident";

export const NIST_PHASE_ORDER: IncidentPhase[] = [
  "new",
  "detection_analysis",
  "containment",
  "eradication",
  "recovery",
  "post_incident",
];

export type IncidentPriority = "p1" | "p2" | "p3" | "p4";

export interface Incident {
  id: string;
  tenantId: string;
  title: string;
  description: string;
  severity: Severity;
  priority: IncidentPriority;
  phase: IncidentPhase;
  // Resolved via a live join server-side (see backend's
  // IncidentRepository.AssigneesForIncidents) -- zero or more analysts.
  assignees: UserSummary[];
  tags: string[];
  slaDueAt?: string;
  slaBreached: boolean;
  openedAt: string;
  closedAt?: string;
  createdAt: string;
  updatedAt: string;
}

// IncidentSLAPolicy mirrors backend/internal/domain.IncidentSLAPolicy --
// Settings -> Incident SLAs. No row for a (severity, priority) pair means
// unconfigured, not zero minutes.
export interface IncidentSLAPolicy {
  id: string;
  tenantId: string;
  severity: Severity;
  priority: IncidentPriority;
  dueWithinMinutes: number;
  createdAt: string;
  updatedAt: string;
}

export interface IncidentStatusHistoryEntry {
  id: string;
  incidentId: string;
  tenantId: string;
  phase: IncidentPhase;
  enteredAt: string;
  correctedEnteredAt?: string;
  correctedAt?: string;
  correctedBy?: string;
  correctionReason?: string;
  createdAt: string;
}

export type IncidentEventType =
  | "created"
  | "phase_changed"
  | "phase_skipped"
  | "closed"
  | "severity_priority_changed"
  | "description_edited"
  | "tags_changed"
  | "alert_linked"
  | "alert_unlinked"
  | "ai_analysis_run"
  | "status_timestamp_corrected"
  | "assignees_changed";

export interface IncidentEvent {
  id: number;
  incidentId: string;
  tenantId: string;
  eventType: IncidentEventType;
  actorType: "user" | "system" | "ai";
  actorId?: string;
  data: unknown;
  createdAt: string;
}

export interface IncidentComment {
  id: string;
  incidentId: string;
  tenantId: string;
  authorId: string;
  authorName: string;
  body: string;
  imageUrl?: string;
  createdAt: string;
}
