import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import { useConfirm } from "../../../hooks/useConfirm";
import type { Role, User } from "../../../types/api";
import { RoleSelect } from "./RoleSelect";

export function UserRow({
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
  const { confirming, confirm, cancel } = useConfirm<"reset" | "revoke">();

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
    cancel();
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
    cancel();
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
              onClick={() => confirm("reset")}
              disabled={resetting || user.authProvider !== "local"}
              title={
                user.authProvider !== "local"
                  ? (t("settings.users.federatedNoReset", { name: user.name, provider: user.authProvider }) ?? undefined)
                  : undefined
              }
            >
              {resetting ? t("settings.users.resetting") : t("settings.users.resetPassword")}
            </button>
            <button className="btn btn-sm" onClick={() => confirm("revoke")} disabled={revoking}>
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
            <button className="btn btn-ghost btn-sm" onClick={cancel}>
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
            <button className="btn btn-ghost btn-sm" onClick={cancel}>
              {t("common.cancel")}
            </button>
          </>
        )}

        {revoked && <span className="helper-text">{t("settings.users.revokedDone")}</span>}
      </div>
    </div>
  );
}
