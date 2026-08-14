// Mirrors backend/internal/domain/playbook.go.
import type { IncidentPhase } from "./incidents";

export interface PlaybookStep {
  id: string;
  text: string;
  // Non-empty webhookUrl means this step can fire a real outbound POST --
  // the UI only surfaces the toggle for containment-phase steps, but the
  // shape allows it on any phase (matches the backend, which doesn't
  // enforce phase structurally either).
  webhookUrl?: string;
  webhookPayloadTemplate?: string;
}

export interface Playbook {
  id: string;
  tenantId: string;
  title: string;
  category: string;
  // alertNamePattern is a SQL LIKE-style pattern ("%" wildcard), ILIKE-matched
  // against an alert's title at ingest time to auto-assign this playbook.
  // Empty means it never matches by name, only via isDefault.
  alertNamePattern: string;
  // isDefault marks the one playbook per tenant assigned to an alert when no
  // alertNamePattern matches -- at most one playbook can have this set.
  isDefault: boolean;
  description: string;
  keywords: string[];
  steps: Partial<Record<IncidentPhase, PlaybookStep[]>>;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}
