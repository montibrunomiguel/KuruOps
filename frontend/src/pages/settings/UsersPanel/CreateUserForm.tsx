import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { CreatedUser, Role } from "../../../types/api";
import { RoleSelect } from "./RoleSelect";

export function CreateUserForm({
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
