import { useId } from "react";
import { useTranslation } from "react-i18next";
import type { MCPAuthType, MCPServer } from "../../../types/api";

// AuthDraft is the form state for a server's authentication. Secrets live here
// only until the form is submitted: they are sent once, stored encrypted by the
// backend, and never come back -- so an edit form always starts with them blank.
export interface AuthDraft {
  type: MCPAuthType;
  apiKeyHeader: string;
  apiKey: string;
  bearerToken: string;
  oauthTokenUrl: string;
  oauthClientId: string;
  oauthClientSecret: string;
}

export const EMPTY_AUTH_DRAFT: AuthDraft = {
  type: "none",
  apiKeyHeader: "",
  apiKey: "",
  bearerToken: "",
  oauthTokenUrl: "",
  oauthClientId: "",
  oauthClientSecret: "",
};

// authDraftFor seeds an edit form from a saved server: the non-secret
// parameters are filled in, every secret is left blank.
export function authDraftFor(server: MCPServer): AuthDraft {
  return {
    ...EMPTY_AUTH_DRAFT,
    type: server.authType,
    apiKeyHeader: server.authHeaderName ?? "",
    oauthTokenUrl: server.oauthTokenUrl ?? "",
    oauthClientId: server.oauthClientId ?? "",
  };
}

// authPayload builds the request fields for a draft: only the fields that
// belong to the chosen type, and a blank secret is omitted entirely (never sent
// as ""), which on update means "keep the stored one".
export function authPayload(draft: AuthDraft): Record<string, string> {
  switch (draft.type) {
    case "api_key":
      return {
        authType: "api_key",
        apiKeyHeader: draft.apiKeyHeader.trim(),
        ...(draft.apiKey ? { apiKey: draft.apiKey } : {}),
      };
    case "bearer":
      return { authType: "bearer", ...(draft.bearerToken ? { bearerToken: draft.bearerToken } : {}) };
    case "oauth":
      return {
        authType: "oauth",
        oauthTokenUrl: draft.oauthTokenUrl.trim(),
        oauthClientId: draft.oauthClientId.trim(),
        ...(draft.oauthClientSecret ? { oauthClientSecret: draft.oauthClientSecret } : {}),
      };
    default:
      return { authType: "none" };
  }
}

const AUTH_TYPES: MCPAuthType[] = ["none", "api_key", "bearer", "oauth"];

// AuthFields renders the auth type selector and the inputs that type needs.
// `saved` is the server's current config when editing (undefined on create):
// when the chosen type matches what's saved, a blank secret is allowed and
// means "keep", so the secret input is optional and says so.
export function AuthFields({
  value,
  onChange,
  saved,
}: {
  value: AuthDraft;
  onChange: (next: AuthDraft) => void;
  saved?: MCPServer;
}) {
  const { t } = useTranslation();
  const id = useId();
  const set = (patch: Partial<AuthDraft>) => onChange({ ...value, ...patch });
  // A stored secret can be kept only if the type is unchanged.
  const canKeep = saved !== undefined && saved.authType === value.type && value.type !== "none";
  const secretRequired = !canKeep;
  const secretPlaceholder = canKeep ? "••••••••" : undefined;
  const secretHint = canKeep ? t("settings.mcp.auth.keepHint") : t("settings.mcp.auth.secretNote");

  return (
    <div className="field field-full">
      <label htmlFor={`${id}-type`}>{t("settings.mcp.auth.label")}</label>
      <select
        id={`${id}-type`}
        className="select"
        value={value.type}
        onChange={(e) => set({ type: e.target.value as MCPAuthType })}
      >
        {AUTH_TYPES.map((type) => (
          <option key={type} value={type}>
            {t(`settings.mcp.auth.types.${type}`)}
          </option>
        ))}
      </select>

      {value.type === "api_key" && (
        <div className="form-grid" style={{ marginTop: 10 }}>
          <div className="field">
            <label htmlFor={`${id}-header`}>{t("settings.mcp.auth.apiKeyHeader")}</label>
            <input
              id={`${id}-header`}
              className="input"
              placeholder={t("settings.mcp.auth.apiKeyHeaderPlaceholder")}
              value={value.apiKeyHeader}
              onChange={(e) => set({ apiKeyHeader: e.target.value })}
              autoComplete="off"
              spellCheck={false}
              required
            />
          </div>
          <div className="field">
            <label htmlFor={`${id}-key`}>{t("settings.mcp.auth.apiKey")}</label>
            <input
              id={`${id}-key`}
              className="input"
              type="password"
              placeholder={secretPlaceholder}
              value={value.apiKey}
              onChange={(e) => set({ apiKey: e.target.value })}
              autoComplete="new-password"
              required={secretRequired}
            />
          </div>
        </div>
      )}

      {value.type === "bearer" && (
        <div className="field" style={{ marginTop: 10 }}>
          <label htmlFor={`${id}-token`}>{t("settings.mcp.auth.bearerToken")}</label>
          <input
            id={`${id}-token`}
            className="input"
            type="password"
            placeholder={secretPlaceholder}
            value={value.bearerToken}
            onChange={(e) => set({ bearerToken: e.target.value })}
            autoComplete="new-password"
            required={secretRequired}
          />
        </div>
      )}

      {value.type === "oauth" && (
        <div className="form-grid" style={{ marginTop: 10 }}>
          <div className="field field-full">
            <label htmlFor={`${id}-token-url`}>{t("settings.mcp.auth.oauthTokenUrl")}</label>
            <input
              id={`${id}-token-url`}
              className="input"
              type="url"
              placeholder={t("settings.mcp.auth.oauthTokenUrlPlaceholder")}
              value={value.oauthTokenUrl}
              onChange={(e) => set({ oauthTokenUrl: e.target.value })}
              autoComplete="off"
              required
            />
            <span className="field-hint">{t("settings.mcp.auth.oauthHint")} {t("settings.mcp.auth.oauthHttpsHint")}</span>
          </div>
          <div className="field">
            <label htmlFor={`${id}-client-id`}>{t("settings.mcp.auth.oauthClientId")}</label>
            <input
              id={`${id}-client-id`}
              className="input"
              value={value.oauthClientId}
              onChange={(e) => set({ oauthClientId: e.target.value })}
              autoComplete="off"
              spellCheck={false}
              required
            />
          </div>
          <div className="field">
            <label htmlFor={`${id}-client-secret`}>{t("settings.mcp.auth.oauthClientSecret")}</label>
            <input
              id={`${id}-client-secret`}
              className="input"
              type="password"
              placeholder={secretPlaceholder}
              value={value.oauthClientSecret}
              onChange={(e) => set({ oauthClientSecret: e.target.value })}
              autoComplete="new-password"
              required={secretRequired}
            />
          </div>
        </div>
      )}

      {value.type !== "none" && (
        <span className="field-hint">
          {secretHint}
          {saved !== undefined && saved.authType !== "none" && ` ${t("settings.mcp.auth.reenterHint")}`}
        </span>
      )}
    </div>
  );
}
