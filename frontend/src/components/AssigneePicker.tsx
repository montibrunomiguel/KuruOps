import { useTranslation } from "react-i18next";
import { api } from "../api/client";
import { useList } from "../api/hooks";
import type { UserSummary } from "../types/users";

// Sources its options from GET /api/v1/users/directory (the same minimal
// {id, name} projection IncidentsListPage's create form and AlertDetailPage's
// assignee select already use) instead of accepting free text -- an incident
// can only be assigned to a real, active user. Modeled directly on
// TagPicker's controlled value/onChange + chip + dropdown shape.
export function AssigneePicker({
  value,
  onChange,
  disabled,
}: {
  value: string[];
  onChange: (next: string[]) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const { data: directory } = useList<UserSummary>((tk) => api.get<UserSummary[]>("/api/v1/users/directory", tk));
  const byId = new Map((directory ?? []).map((u) => [u.id, u.name]));
  const available = (directory ?? []).filter((u) => !value.includes(u.id));

  function remove(id: string) {
    onChange(value.filter((v) => v !== id));
  }

  return (
    <div>
      {value.length > 0 && (
        <div className="tag-chip-list" style={{ marginBottom: available.length > 0 && !disabled ? 8 : 0 }}>
          {value.map((id) => (
            <span className="tag-chip" key={id}>
              {byId.get(id) ?? id}
              {!disabled && (
                <button type="button" onClick={() => remove(id)} aria-label={t("common.removeAssignee", { name: byId.get(id) ?? id })}>
                  ×
                </button>
              )}
            </span>
          ))}
        </div>
      )}
      {!disabled && available.length > 0 && (
        <select
          className="select"
          value=""
          onChange={(e) => {
            if (e.target.value) onChange([...value, e.target.value]);
          }}
        >
          <option value="">{t("common.addAssigneeOption")}</option>
          {available.map((u) => (
            <option key={u.id} value={u.id}>
              {u.name}
            </option>
          ))}
        </select>
      )}
      {!disabled && (directory ?? []).length === 0 && <p className="helper-text">{t("common.noAssigneesDirectory")}</p>}
    </div>
  );
}
