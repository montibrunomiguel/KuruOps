import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import { useConfirm } from "../../hooks/useConfirm";
import { TagPicker } from "../../components/TagPicker";
import type { ResourceCapability, Role } from "../../types/api";
import { RESOURCE_CAPABILITIES } from "../../types/api";

// ResourceAccessCheckboxes -- resourceAccess is a capability set (see
// domain.ResourceAccess on the backend), not a mutually-exclusive choice, so
// a role can be scoped to just Follow-up, to all three, etc. Lives here
// (rather than UsersPanel) since a Role is now the only place
// resourceAccess is actually edited -- users and group mappings just pick a
// Role.
function ResourceAccessCheckboxes({
  value,
  onChange,
}: {
  value: ResourceCapability[];
  onChange: (next: ResourceCapability[]) => void;
}) {
  const { t } = useTranslation();
  function toggle(cap: ResourceCapability) {
    onChange(value.includes(cap) ? value.filter((c) => c !== cap) : [...value, cap]);
  }

  return (
    <div style={{ display: "flex", gap: 10 }}>
      {RESOURCE_CAPABILITIES.map((cap) => (
        <label key={cap} className="checkbox-row" style={{ fontSize: 12 }}>
          <input type="checkbox" checked={value.includes(cap)} onChange={() => toggle(cap)} />
          {t(`settings.users.capability.${cap}`)}
        </label>
      ))}
    </div>
  );
}

// Settings -> Roles: named, reusable bundles of admin access + resource
// capabilities + tag scope (see backend domain.Role) -- build "SOC L1" or
// "Analyst - Acme" once, assign it to any number of users or LDAP/SAML
// group mappings, instead of re-picking the same three settings on every
// user individually.
export function RolesPanel() {
  const { t } = useTranslation();
  const { data: roles, loading, error, reload } = useList<Role>((tk) =>
    api.get<Role[]>("/api/v1/settings/roles", tk),
  );
  const [creating, setCreating] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);

  return (
    <div className="panel">
      <div className="panel-header">
        <div>
          <h2 className="panel-title">{t("settings.roles.title")}</h2>
          <p className="helper-text" style={{ marginTop: 4 }}>
            {t("settings.roles.helper")}
          </p>
        </div>
        {!creating && (
          <button className="btn btn-primary btn-sm" onClick={() => setCreating(true)}>
            {t("settings.roles.newRole")}
          </button>
        )}
      </div>

      {error && <div className="error-banner">{error}</div>}

      {creating && (
        <RoleForm
          onCancel={() => setCreating(false)}
          onSaved={() => {
            setCreating(false);
            reload();
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && roles && roles.length === 0 && !creating && (
        <div className="empty-state">{t("settings.roles.noRoles")}</div>
      )}
      {!loading &&
        roles &&
        roles.map((role) =>
          editingId === role.id ? (
            <RoleForm
              key={role.id}
              role={role}
              onCancel={() => setEditingId(null)}
              onSaved={() => {
                setEditingId(null);
                reload();
              }}
            />
          ) : (
            <RoleRow key={role.id} role={role} onEdit={() => setEditingId(role.id)} onChanged={reload} />
          ),
        )}
    </div>
  );
}

function RoleRow({ role, onEdit, onChanged }: { role: Role; onEdit: () => void; onChanged: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { confirming, confirm, cancel } = useConfirm();
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function remove() {
    setDeleting(true);
    setError(null);
    try {
      await api.del(`/api/v1/settings/roles/${role.id}`, token);
      cancel();
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setDeleting(false);
    }
  }

  return (
    <div className="row" style={{ alignItems: "flex-start", flexWrap: "wrap" }}>
      <div className="row-main" style={{ minWidth: 220 }}>
        <p className="row-title">
          {role.name} {role.isAdmin && <span className="badge badge-success">{t("settings.roles.adminBadge")}</span>}
        </p>
        <p className="row-sub">
          {role.resourceAccess.length
            ? role.resourceAccess.map((c) => t(`settings.users.capability.${c}`)).join(", ")
            : t("settings.roles.noResourceAccess")}
          {" · "}
          {t("settings.roles.tagsLabel")}: {role.allowedTags.length ? role.allowedTags.join(", ") : t("settings.roles.allTags")}
        </p>
        {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
      </div>

      <div className="row-actions">
        <button className="btn btn-sm" onClick={onEdit}>
          {t("common.edit")}
        </button>
        {confirming ? (
          <>
            <span className="helper-text">{t("settings.roles.removeConfirm")}</span>
            <button className="btn btn-danger btn-sm" onClick={remove} disabled={deleting}>
              {deleting ? t("common.saving") : t("common.confirmDelete")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={cancel} disabled={deleting}>
              {t("common.cancel")}
            </button>
          </>
        ) : (
          <button className="btn btn-danger btn-sm" onClick={() => confirm()}>
            {t("common.remove")}
          </button>
        )}
      </div>
    </div>
  );
}

function RoleForm({ role, onCancel, onSaved }: { role?: Role; onCancel: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState(role?.name ?? "");
  const [isAdmin, setIsAdmin] = useState(role?.isAdmin ?? false);
  const [resourceAccess, setResourceAccess] = useState<ResourceCapability[]>(role?.resourceAccess ?? ["alerts", "incidents"]);
  const [allowedTags, setAllowedTags] = useState<string[]>(role?.allowedTags ?? []);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const body = { name, isAdmin, resourceAccess, allowedTags };
      if (role) {
        await api.put(`/api/v1/settings/roles/${role.id}`, body, token);
      } else {
        await api.post("/api/v1/settings/roles", body, token);
      }
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 14 }}>
      {error && <div className="error-banner">{error}</div>}
      <div className="form-grid">
        <div className="field">
          <label htmlFor="role-name">{t("settings.roles.form.name")}</label>
          <input id="role-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="field">
          <label className="checkbox-row" style={{ marginTop: 22 }}>
            <input type="checkbox" checked={isAdmin} onChange={(e) => setIsAdmin(e.target.checked)} />
            {t("settings.roles.form.isAdmin")}
          </label>
          <span className="field-hint">{t("settings.roles.form.isAdminHint")}</span>
        </div>
        <div className="field">
          <label>{t("settings.roles.form.resourceAccess")}</label>
          <ResourceAccessCheckboxes value={resourceAccess} onChange={setResourceAccess} />
        </div>
        <div className="field field-full">
          <label>
            {t("settings.roles.form.allowedTags")}{" "}
            <span className="field-hint">{t("settings.roles.form.allowedTagsHint")}</span>
          </label>
          <TagPicker value={allowedTags} onChange={setAllowedTags} />
        </div>
      </div>
      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel} disabled={submitting}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}
