import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { useList, mutationErrorMessage } from "../../../api/hooks";
import type { Incident, IncidentRole } from "../../../types/incidents";
import { INCIDENT_ROLE_ORDER, SINGLE_ASSIGNEE_ROLES } from "../../../types/incidents";
import type { UserSummary } from "../../../types/users";
import { AssigneePicker } from "../../../components/AssigneePicker";

// IncidentRolesPanel is the NIST 800-61 "Team Roles" section -- the only
// per-person assignment UI on the incident detail page (the generic
// assignees panel that used to sit alongside it was removed; Commander/
// Technical Lead/etc. now fully replace it). Commander/Technical Lead
// render as a plain single-select (at most one person); the other three
// reuse AssigneePicker, same auto-save-on-change UX as IncidentTagsRow.
export function IncidentRolesPanel({ incident, onSaved }: { incident: Incident; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: directory } = useList<UserSummary>(["users-directory"], (tk) => api.get<UserSummary[]>("/api/v1/users/directory", tk));
  const [error, setError] = useState<string | null>(null);
  const [submittingRole, setSubmittingRole] = useState<IncidentRole | null>(null);

  function usersFor(role: IncidentRole): string[] {
    return incident.roles.filter((r) => r.role === role).map((r) => r.user.id);
  }

  async function save(role: IncidentRole, userIds: string[]) {
    setSubmittingRole(role);
    setError(null);
    try {
      await api.put(`/api/v1/incidents/${incident.id}/roles/${role}`, { userIds }, token);
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmittingRole(null);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 12 }}>
        {t("incidents.roles.title")}
      </h2>
      {error && <div className="error-banner" style={{ marginBottom: 10 }}>{error}</div>}
      {INCIDENT_ROLE_ORDER.map((role) => {
        const isSingleAssignee = SINGLE_ASSIGNEE_ROLES.includes(role);
        const current = usersFor(role);
        const disabled = submittingRole === role;
        return (
          <div key={role} style={{ marginBottom: 14 }}>
            <label htmlFor={`incident-role-${role}`} style={{ display: "block", marginBottom: 4, fontWeight: 600, fontSize: 13 }}>
              {t(`incidents.roles.role.${role}`)}
            </label>
            {isSingleAssignee ? (
              <select
                id={`incident-role-${role}`}
                className="select"
                value={current[0] ?? ""}
                disabled={disabled}
                onChange={(e) => save(role, e.target.value ? [e.target.value] : [])}
              >
                <option value="">{t("incidents.roles.unassigned")}</option>
                {(directory ?? []).map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.name}
                  </option>
                ))}
              </select>
            ) : (
              <AssigneePicker value={current} onChange={(next) => save(role, next)} disabled={disabled} directory={directory} />
            )}
          </div>
        );
      })}
    </div>
  );
}
