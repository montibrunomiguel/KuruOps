import { api } from "../api/client";
import { useList } from "../api/hooks";
import type { UserSummary } from "../types/users";

// Shared single-select filter sourced from GET /api/v1/users/directory --
// used for both the Alerts tab's analyst filter and the Incidents tab's
// commander filter (see AssigneePicker for the same data source used as a
// multi-select instead).
export function PersonFilter({
  value,
  onChange,
  allLabel,
  ariaLabel,
}: {
  value: string;
  onChange: (v: string) => void;
  allLabel: string;
  ariaLabel?: string;
}) {
  const { data: directory } = useList<UserSummary>(["users-directory"], (tk) => api.get<UserSummary[]>("/api/v1/users/directory", tk));
  return (
    <select className="select" aria-label={ariaLabel} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{allLabel}</option>
      {(directory ?? []).map((u) => (
        <option key={u.id} value={u.id}>
          {u.name}
        </option>
      ))}
    </select>
  );
}
