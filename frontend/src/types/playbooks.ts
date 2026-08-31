// Mirrors backend/internal/domain/playbook.go.
import { NIST_PHASE_ORDER, type IncidentPhase } from "./incidents";

// PLAYBOOK_PHASES: every NIST phase a playbook can have steps for, except
// "new" -- by the time an incident is in New/Identification, an analyst
// hasn't triaged it yet, so there's nothing here for a *response* playbook
// to prescribe (steps only make sense from Detection & Analysis onward).
// The backend itself doesn't restrict which phase a playbook_phase_steps
// row can use (matches NIST_PHASE_ORDER's full set, see
// backend/internal/domain/playbook.go's Steps field) -- this is a
// UI-only scoping of what PlaybookDetailPage/PlaybookViewModal offer to
// add to or display, not a data-model restriction.
export const PLAYBOOK_PHASES: IncidentPhase[] = NIST_PHASE_ORDER.filter((p) => p !== "new");

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
