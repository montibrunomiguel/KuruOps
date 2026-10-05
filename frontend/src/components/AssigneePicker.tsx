import { useTranslation } from "react-i18next";
import { api } from "../api/client";
import { useList } from "../api/hooks";
import type { UserSummary } from "../types/users";

// Sources its options from GET /api/v1/users/directory (the same minimal
// {id, name} projection IncidentsListPage's create form and AlertDetailPage's
// assignee select already use) instead of accepting free text -- an incident
// can only be assigned to a real, active user. Modeled directly on
// TagPicker's controlled value/onChange + chip + dropdown shape.
//
// `directory` is optional: pass it down (even as `null` while it's still
// loading) when a parent already fetches the user list -- e.g.
// IncidentRolesPanel renders several of these side by side and shares one
// fetch instead of each picker re-fetching independently. Leave the prop
// out entirely and this component fetches its own copy, for standalone
// callers like IncidentsListPage's create form. The distinction has to be
// undefined (prop absent -> self-fetch) vs. null (prop present but its
// fetch hasn't resolved yet -> wait, don't self-fetch either) -- collapsing
// both to "falsy" would make every picker fire its own request during the
// parent's loading window, before the shared value ever arrives.
export function AssigneePicker({
  value,
  onChange,
  disabled,
  directory: directoryProp,
}: {
  value: string[];
  onChange: (next: string[]) => void;
  disabled?: boolean;
  directory?: UserSummary[] | null;
}) {
  const { t } = useTranslation();
  const sharesDirectory = directoryProp !== undefined;
  // Skips the network call (resolves to [] locally instead) when a parent
  // already owns the fetch -- still calls useList unconditionally so this
  // obeys the rules of hooks either way.
  const { data: fetchedDirectory } = useList<UserSummary>(
    ["users-directory", sharesDirectory],
    (tk) => (sharesDirectory ? Promise.resolve<UserSummary[]>([]) : api.get<UserSummary[]>("/api/v1/users/directory", tk)),
  );
  const directory = sharesDirectory ? directoryProp : fetchedDirectory;
  const byId = new Map((directory ?? []).map((u) => [u.id, u.name]));
  const available = (directory ?? []).filter((u) => !value.includes(u.id));

  function remove(id: string) {
    onChange(value.filter((v) => v !== id));
  }

  return (
    <div>
      {/* Same .tag-picker/.tag-chip/.tag-picker-add markup TagPicker uses --
          chips and the trailing <select> as one bordered box instead of a
          bare chip list stacked above a separate <select>, which used to
          read as two disconnected controls rather than one. */}
      <div className="tag-picker">
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
        {!disabled && available.length > 0 && (
          <select
            className="tag-picker-add"
            aria-label={t("common.addAssigneeOption")}
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
      </div>
      {!disabled && (directory ?? []).length === 0 && (
        <p className="helper-text" style={{ marginTop: 6 }}>
          {t("common.noAssigneesDirectory")}
        </p>
      )}
    </div>
  );
}
