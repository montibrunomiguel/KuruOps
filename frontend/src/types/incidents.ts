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

// NIST 800-61 team roles -- additive to `assignees` above, not a
// replacement (see backend domain.Incident.Roles's doc comment). Commander
// and Technical Lead are single-assignee; the rest allow any number of people.
export type IncidentRole =
  | "commander"
  | "incident_handler"
  | "communications_lead"
  | "privacy_officer"
  | "technical_lead";

// Display order for the "Team Roles" section -- single-assignee roles first.
export const INCIDENT_ROLE_ORDER: IncidentRole[] = [
  "commander",
  "technical_lead",
  "incident_handler",
  "communications_lead",
  "privacy_officer",
];

export const SINGLE_ASSIGNEE_ROLES: IncidentRole[] = ["commander", "technical_lead"];

export interface IncidentRoleAssignment {
  role: IncidentRole;
  user: UserSummary;
}

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
  // NIST team-role assignments -- see IncidentRole above.
  roles: IncidentRoleAssignment[];
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
  | "assignees_changed"
  | "role_assigned"
  | "role_unassigned";

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
