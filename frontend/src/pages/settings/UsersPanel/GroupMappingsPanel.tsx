import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { useList, mutationErrorMessage } from "../../../api/hooks";
import { useConfirm } from "../../../hooks/useConfirm";
import type { AuthGroupMapping, Role } from "../../../types/api";
import { GroupMappingForm } from "./GroupMappingForm";

export function GroupMappingsPanel({ roles }: { roles: Role[] }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: mappings, loading, error, reload } = useList<AuthGroupMapping>((tk) =>
    api.get<AuthGroupMapping[]>("/api/v1/settings/users/group-mappings", tk),
  );
  const [showCreate, setShowCreate] = useState(false);
  const { confirming: confirmingId, confirm: confirmId, cancel: cancelId } = useConfirm<string>();
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function remove(id: string) {
    setDeletingId(id);
    setDeleteError(null);
    try {
      await api.del(`/api/v1/settings/users/group-mappings/${id}`, token);
      cancelId();
      reload();
    } catch (err) {
      setDeleteError(mutationErrorMessage(err));
    } finally {
      setDeletingId(null);
    }
  }

  return (
    <div className="panel">
      <div className="panel-header">
        <div>
          <h2 className="panel-title">{t("settings.users.groupMappings.title")}</h2>
          <p className="helper-text" style={{ marginTop: 4 }}>
            {t("settings.users.groupMappings.helper")}
          </p>
        </div>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("settings.users.groupMappings.newMapping")}
        </button>
      </div>

      {error && <div className="error-banner">{error}</div>}
      {deleteError && <div className="error-banner">{deleteError}</div>}

      {showCreate && (
        <GroupMappingForm
          roles={roles}
          onCancel={() => setShowCreate(false)}
          onSaved={() => {
            setShowCreate(false);
            reload();
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && mappings && mappings.length === 0 && (
        <div className="empty-state">{t("settings.users.groupMappings.noMappings")}</div>
      )}
      {!loading &&
        mappings &&
        mappings.map((m) => (
          <div key={m.id} className="row">
            <div className="row-main">
              <p className="row-title">
                {m.externalGroup} <span className="badge badge-muted">{m.provider}</span>
              </p>
              <p className="row-sub">
                {m.role.name} ·{" "}
                {m.role.resourceAccess.map((c) => t(`settings.users.capability.${c}`)).join(", ") ||
                  t("settings.users.groupMappings.noAccess")}{" "}
                · tags: {m.role.allowedTags.length ? m.role.allowedTags.join(", ") : t("settings.users.groupMappings.allTags")}
              </p>
            </div>
            <div className="row-actions">
              {confirmingId === m.id ? (
                <>
                  <span className="helper-text" style={{ flexBasis: "100%" }}>
                    {t("settings.users.groupMappings.removeConfirm")}
                  </span>
                  <button
                    className="btn btn-danger btn-sm"
                    disabled={deletingId === m.id}
                    onClick={() => remove(m.id)}
                  >
                    {deletingId === m.id ? t("common.saving") : t("common.confirmDelete")}
                  </button>
                  <button className="btn btn-ghost btn-sm" onClick={cancelId}>
                    {t("common.cancel")}
                  </button>
                </>
              ) : (
                <button className="btn btn-danger btn-sm" onClick={() => confirmId(m.id)}>
                  {t("common.remove")}
                </button>
              )}
            </div>
          </div>
        ))}
    </div>
  );
}
