// Mirrors backend/internal/domain/playbook.go.
import type { IncidentPhase } from "./incidents";

export interface Playbook {
  id: string;
  tenantId: string;
  title: string;
  category: string;
  description: string;
  keywords: string[];
  steps: Partial<Record<IncidentPhase, string[]>>;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}
