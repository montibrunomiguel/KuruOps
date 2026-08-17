import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { Role } from "../../../types/api";
import { RoleSelect } from "./RoleSelect";

export function GroupMappingForm({
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
