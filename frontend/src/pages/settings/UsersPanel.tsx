import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../api/client";
import { useList } from "../../api/hooks";
import type { CreatedUser, Role, User } from "../../types/api";
import { TempPasswordBanner } from "./UsersPanel/TempPasswordBanner";
import { CreateUserForm } from "./UsersPanel/CreateUserForm";
import { UserRow } from "./UsersPanel/UserRow";
import { GroupMappingsPanel } from "./UsersPanel/GroupMappingsPanel";

export function UsersPanel() {
  const { t } = useTranslation();
  const { data: users, loading, error, reload } = useList<User>(["settings-users"], (tk) =>
    api.get<User[]>("/api/v1/settings/users", tk),
  );
  const { data: roles } = useList<Role>(["settings-roles"], (tk) => api.get<Role[]>("/api/v1/settings/roles", tk));
  const [showCreate, setShowCreate] = useState(false);
  const [created, setCreated] = useState<CreatedUser | null>(null);
  const [resetResult, setResetResult] = useState<{ name: string; temporaryPassword: string } | null>(null);

  return (
    <>
      <div className="panel">
        <div className="panel-header">
          <h2 className="panel-title">{t("settings.users.title")}</h2>
          <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
            {t("settings.users.newUser")}
          </button>
        </div>
        {error && <div className="error-banner">{error}</div>}

        {created && (
          <TempPasswordBanner
            title={t("settings.users.createdTitle", { name: created.user.name })}
            helper={t("settings.users.createdHelper")}
            temporaryPassword={created.temporaryPassword}
            onClose={() => setCreated(null)}
          />
        )}

        {resetResult && (
          <TempPasswordBanner
            title={t("settings.users.resetTitle", { name: resetResult.name })}
            helper={t("settings.users.resetHelper")}
            temporaryPassword={resetResult.temporaryPassword}
            onClose={() => setResetResult(null)}
          />
        )}

        {showCreate && (
          <CreateUserForm
            roles={roles ?? []}
            onCancel={() => setShowCreate(false)}
            onCreated={(result) => {
              setShowCreate(false);
              setCreated(result);
              reload();
            }}
          />
        )}

        {loading && <div className="empty-state">{t("common.loading")}</div>}
        {!loading && users && users.length === 0 && <div className="empty-state">{t("settings.users.noUsers")}</div>}
        {!loading &&
          users &&
          users.map((u) => (
            <UserRow
              key={u.id}
              user={u}
              roles={roles ?? []}
              onChanged={reload}
              onReset={(result) => setResetResult(result)}
            />
          ))}
      </div>

      <GroupMappingsPanel roles={roles ?? []} />
    </>
  );
}
