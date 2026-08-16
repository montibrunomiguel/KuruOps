import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { AuthGroupMapping, CreatedUser, Role, User } from "../../types/api";

// RoleSelect is the one control every user/group-mapping edit needs now --
// picking one of the tenant's Roles (see Settings -> Roles) instead of the
// old inline role/resourceAccess/allowedTags trio.
function RoleSelect({
  id,
  roles,
  value,
  onChange,
}: {
  id: string;
  roles: Role[];
  value: string;
  onChange: (roleId: string) => void;
}) {
  return (
    <select id={id} className="select" value={value} onChange={(e) => onChange(e.target.value)} required>
      <option value="" disabled>
        {roles.length ? "—" : ""}
      </option>
      {roles.map((r) => (
        <option key={r.id} value={r.id}>
          {r.name}
        </option>
      ))}
    </select>
  );
}

// TempPasswordBanner is the "show a one-time password exactly once" UX
// shared by user creation and password reset -- same admin-shares-it-out-of-
// band, forced-change-on-next-login flow either way, so the display is
// identical, only the title/helper text differ.
function TempPasswordBanner({
  title,
  helper,
  temporaryPassword,
  onClose,
}: {
  title: string;
  helper: string;
  temporaryPassword: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="panel" style={{ marginBottom: 14, borderColor: "var(--accent)" }}>
      <p className="row-title" style={{ marginBottom: 4 }}>
        {title}
      </p>
      <p className="helper-text" style={{ marginBottom: 10 }}>
        {helper}
      </p>
      <code className="mono" style={{ display: "block", padding: 8, background: "var(--surface-2)", borderRadius: 6, wordBreak: "break-all" }}>
        {temporaryPassword}
      </code>
      <div className="row-actions" style={{ marginTop: 10 }}>
        <button className="btn btn-sm" onClick={onClose}>
          {t("common.close")}
        </button>
      </div>
    </div>
  );
}

export function UsersPanel() {
  const { t } = useTranslation();
  const { data: users, loading, error, reload } = useList<User>((tk) =>
    api.get<User[]>("/api/v1/settings/users", tk),
  );
  const { data: roles } = useList<Role>((tk) => api.get<Role[]>("/api/v1/settings/roles", tk));
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

function CreateUserForm({
  roles,
  onCancel,
  onCreated,
}: {
  roles: Role[];
  onCancel: () => void;
  onCreated: (result: CreatedUser) => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [roleId, setRoleId] = useState(roles[0]?.id ?? "");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const result = await api.post<CreatedUser>("/api/v1/settings/users", { email, name, phone, roleId }, token);
      onCreated(result);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 14 }}>
      {error && <div className="error-banner">{error}</div>}
      {roles.length === 0 && (
        <p className="helper-text" style={{ marginBottom: 10 }}>
          {t("settings.users.form.noRolesYet")}
        </p>
      )}
      <div className="form-grid">
        <div className="field">
          <label htmlFor="nu-email">{t("settings.users.form.email")}</label>
          <input
            id="nu-email"
            type="email"
            className="input"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="nu-name">{t("settings.users.form.name")}</label>
          <input id="nu-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="nu-phone">{t("settings.users.form.phone")}</label>
          <input id="nu-phone" type="tel" className="input" value={phone} onChange={(e) => setPhone(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="nu-role">{t("settings.users.form.role")}</label>
          <RoleSelect id="nu-role" roles={roles} value={roleId} onChange={setRoleId} />
        </div>
      </div>
      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting || roles.length === 0}>
          {submitting ? t("common.creating") : t("common.create")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}

function UserRow({
  user,
  roles,
  onChanged,
  onReset,
}: {
  user: User;
  roles: Role[];
  onChanged: () => void;
  onReset: (result: { name: string; temporaryPassword: string }) => void;
}) {
  const { t } = useTranslation();
  const { token, user: me } = useAuth();
  const [roleId, setRoleId] = useState(user.roleId);
  const [phone, setPhone] = useState(user.phone ?? "");
  const [saving, setSaving] = useState(false);
  const [phoneSaving, setPhoneSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [phoneError, setPhoneError] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [phoneDirty, setPhoneDirty] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [revoked, setRevoked] = useState(false);
  // Inline confirm/cancel instead of window.confirm() -- native confirm()
  // dialogs are unreliable (silently auto-dismissed) in some embedded
  // browser contexts, the same reason every other destructive action in
  // Settings uses this pattern. Only one of reset/revoke can be pending
  // confirmation at a time per row.
  const [confirming, setConfirming] = useState<"reset" | "revoke" | null>(null);

  async function save() {
    setSaving(true);
    setError(null);
    try {
      await api.put(`/api/v1/settings/users/${user.id}/access`, { roleId }, token);
      setDirty(false);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function savePhone() {
    setPhoneSaving(true);
    setPhoneError(null);
    try {
      await api.put(`/api/v1/settings/users/${user.id}/phone`, { phone }, token);
      setPhoneDirty(false);
      onChanged();
    } catch (err) {
      setPhoneError(mutationErrorMessage(err));
    } finally {
      setPhoneSaving(false);
    }
  }

  async function toggleActive() {
    setSaving(true);
    setError(null);
    try {
      await api.post(`/api/v1/settings/users/${user.id}/${user.isActive ? "deactivate" : "activate"}`, {}, token);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function resetPassword() {
    setConfirming(null);
    setResetting(true);
    setError(null);
    try {
      const result = await api.post<{ temporaryPassword: string }>(
        `/api/v1/settings/users/${user.id}/reset-password`,
        {},
        token,
      );
      onReset({ name: user.name, temporaryPassword: result.temporaryPassword });
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setResetting(false);
    }
  }

  // revokeSessions cuts off a session the user already holds (e.g. a
  // suspected compromised device) without deactivating the account --
  // see AuthService.RevokeSessions. The access token they're mid-request
  // with still works until its own 15-minute expiry; this only stops it
  // from being renewed via POST /auth/refresh.
  async function revokeSessions() {
    setConfirming(null);
    setRevoking(true);
    setError(null);
    try {
      await api.post(`/api/v1/settings/users/${user.id}/revoke-sessions`, {}, token);
      setRevoked(true);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setRevoking(false);
    }
  }

  return (
    <div className="row" style={{ alignItems: "flex-start", flexWrap: "wrap" }}>
      <div className="row-main" style={{ minWidth: 220 }}>
        <p className="row-title">
          {user.name} {user.id === me?.id && <span className="badge badge-muted">{t("settings.users.youBadge")}</span>}
          {!user.isActive && <span className="badge badge-critical">{t("settings.users.inactiveBadge")}</span>}
        </p>
        <p className="row-sub">
          {user.email} · {user.authProvider}
        </p>
        {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
        {phoneError && <div className="error-banner" style={{ marginTop: 8 }}>{phoneError}</div>}
      </div>

      <div style={{ display: "flex", gap: 12, flexWrap: "wrap", alignItems: "center" }}>
        <RoleSelect
          id={`user-role-${user.id}`}
          roles={roles}
          value={roleId}
          onChange={(next) => {
            setRoleId(next);
            setDirty(true);
          }}
        />
        {dirty && (
          <button className="btn btn-primary btn-sm" onClick={save} disabled={saving}>
            {t("common.save")}
          </button>
        )}
        <input
          id={`user-phone-${user.id}`}
          aria-label={t("settings.users.phoneLabel") ?? undefined}
          className="input"
          style={{ width: 150 }}
          type="tel"
          value={phone}
          onChange={(e) => {
            setPhone(e.target.value);
            setPhoneDirty(true);
          }}
          title={t("settings.users.phoneHint") ?? undefined}
          placeholder={t("settings.users.phonePlaceholder") ?? undefined}
        />
        {phoneDirty && (
          <button className="btn btn-primary btn-sm" onClick={savePhone} disabled={phoneSaving}>
            {t("common.save")}
          </button>
        )}
        <button className="btn btn-sm" onClick={toggleActive} disabled={saving}>
          {user.isActive ? t("settings.users.deactivate") : t("settings.users.activate")}
        </button>

        {confirming === null && (
          <>
            <button
              className="btn btn-sm"
              onClick={() => setConfirming("reset")}
              disabled={resetting || user.authProvider !== "local"}
              title={
                user.authProvider !== "local"
                  ? (t("settings.users.federatedNoReset", { name: user.name, provider: user.authProvider }) ?? undefined)
                  : undefined
              }
            >
              {resetting ? t("settings.users.resetting") : t("settings.users.resetPassword")}
            </button>
            <button className="btn btn-sm" onClick={() => setConfirming("revoke")} disabled={revoking}>
              {revoking ? t("settings.users.revoking") : t("settings.users.revokeSessions")}
            </button>
          </>
        )}

        {confirming === "reset" && (
          <>
            <span className="helper-text">{t("settings.users.resetConfirm", { name: user.name })}</span>
            <button className="btn btn-danger btn-sm" onClick={resetPassword} disabled={resetting}>
              {resetting ? t("common.saving") : t("common.confirm")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setConfirming(null)}>
              {t("common.cancel")}
            </button>
          </>
        )}

        {confirming === "revoke" && (
          <>
            <span className="helper-text">{t("settings.users.revokeSessionsConfirm", { name: user.name })}</span>
            <button className="btn btn-danger btn-sm" onClick={revokeSessions} disabled={revoking}>
              {revoking ? t("common.saving") : t("common.confirm")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setConfirming(null)}>
              {t("common.cancel")}
            </button>
          </>
        )}

        {revoked && <span className="helper-text">{t("settings.users.revokedDone")}</span>}
      </div>
    </div>
  );
}

function GroupMappingsPanel({ roles }: { roles: Role[] }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: mappings, loading, error, reload } = useList<AuthGroupMapping>((tk) =>
    api.get<AuthGroupMapping[]>("/api/v1/settings/users/group-mappings", tk),
  );
  const [showCreate, setShowCreate] = useState(false);
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function remove(id: string) {
    setDeletingId(id);
    setDeleteError(null);
    try {
      await api.del(`/api/v1/settings/users/group-mappings/${id}`, token);
      setConfirmingId(null);
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
                  <button className="btn btn-ghost btn-sm" onClick={() => setConfirmingId(null)}>
                    {t("common.cancel")}
                  </button>
                </>
              ) : (
                <button className="btn btn-danger btn-sm" onClick={() => setConfirmingId(m.id)}>
                  {t("common.remove")}
                </button>
              )}
            </div>
          </div>
        ))}
    </div>
  );
}

function GroupMappingForm({
  roles,
  onCancel,
  onSaved,
}: {
  roles: Role[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [provider, setProvider] = useState<"ldap" | "saml">("ldap");
  const [externalGroup, setExternalGroup] = useState("");
  const [roleId, setRoleId] = useState(roles[0]?.id ?? "");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await api.put(
        `/api/v1/settings/users/group-mappings/${provider}/${encodeURIComponent(externalGroup)}`,
        { roleId },
        token,
      );
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
      {roles.length === 0 && (
        <p className="helper-text" style={{ marginBottom: 10 }}>
          {t("settings.users.form.noRolesYet")}
        </p>
      )}
      <div className="form-grid">
        <div className="field">
          <label htmlFor="gm-provider">{t("settings.users.groupMappings.form.provider")}</label>
          <select
            id="gm-provider"
            className="select"
            value={provider}
            onChange={(e) => setProvider(e.target.value as "ldap" | "saml")}
          >
            <option value="ldap">LDAP</option>
            <option value="saml">SAML</option>
          </select>
        </div>
        <div className="field">
          <label htmlFor="gm-group">
            {t("settings.users.groupMappings.form.group")}{" "}
            {provider === "ldap"
              ? t("settings.users.groupMappings.form.groupLdapHint")
              : t("settings.users.groupMappings.form.groupSamlHint")}
          </label>
          <input
            id="gm-group"
            className="input"
            placeholder={provider === "ldap" ? "cn=SOC-Tier1,ou=groups,dc=acme,dc=local" : "soc-tier1"}
            value={externalGroup}
            onChange={(e) => setExternalGroup(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="gm-role">{t("settings.users.form.role")}</label>
          <RoleSelect id="gm-role" roles={roles} value={roleId} onChange={setRoleId} />
        </div>
      </div>
      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting || roles.length === 0}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}
