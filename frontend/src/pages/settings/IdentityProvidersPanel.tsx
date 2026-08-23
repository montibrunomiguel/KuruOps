import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useConfirm } from "../../hooks/useConfirm";
import { useAdminSingletonConfig } from "../../hooks/useAdminSingletonConfig";
import type { LDAPConfig, SAMLConfig } from "../../types/api";

export function IdentityProvidersPanel() {
  return (
    <>
      <LDAPPanel />
      <SAMLPanel />
    </>
  );
}

function LDAPPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: existing, loading, error, configured, saveError, submitting, saved, save } = useAdminSingletonConfig<LDAPConfig | null>(
    ["identity-provider-ldap"],
    (tok) => api.get<LDAPConfig | null>("/api/v1/settings/identity-providers/ldap", tok),
  );

  const [host, setHost] = useState("");
  const [port, setPort] = useState(636);
  const [useTls, setUseTls] = useState(true);
  const [bindDn, setBindDn] = useState("");
  const [bindPassword, setBindPassword] = useState("");
  const [userBaseDn, setUserBaseDn] = useState("");
  const [userFilter, setUserFilter] = useState("(mail=%s)");
  const [groupBaseDn, setGroupBaseDn] = useState("");
  const [groupAttribute, setGroupAttribute] = useState("memberOf");
  // Inline confirm/cancel instead of window.confirm() -- some embedded
  // browser contexts silently auto-dismiss native confirm() dialogs, which
  // made delete look like it does nothing (see OnCallScheduleDetailPage/TagsPanel).
  const { confirming: confirmingRemove, confirm: confirmRemove, cancel: cancelRemove } = useConfirm();

  useEffect(() => {
    if (existing) {
      setHost(existing.host);
      setPort(existing.port);
      setUseTls(existing.useTls);
      setBindDn(existing.bindDn);
      setUserBaseDn(existing.userBaseDn);
      setUserFilter(existing.userFilter);
      setGroupBaseDn(existing.groupBaseDn);
      setGroupAttribute(existing.groupAttribute);
    }
  }, [existing]);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    await save(async () => {
      await api.put(
        "/api/v1/settings/identity-providers/ldap",
        { host, port, useTls, bindDn, bindPassword, userBaseDn, userFilter, groupBaseDn, groupAttribute },
        token,
      );
      setBindPassword("");
    });
  }

  async function handleRemove() {
    cancelRemove();
    await save(
      async () => {
        await api.del("/api/v1/settings/identity-providers/ldap", token);
        setHost("");
        setPort(636);
        setUseTls(true);
        setBindDn("");
        setBindPassword("");
        setUserBaseDn("");
        setUserFilter("(mail=%s)");
        setGroupBaseDn("");
        setGroupAttribute("memberOf");
      },
      { markSavedOnSuccess: false },
    );
  }

  if (loading) return <div className="panel"><div className="empty-state">{t("common.loading")}</div></div>;

  return (
    <form onSubmit={handleSubmit} className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.identityProviders.ldap.title")}</h2>
        {configured && (
          <span className="badge badge-success">
            <span className="badge-status-dot" />
            {t("settings.identityProviders.ldap.configured")}
          </span>
        )}
      </div>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.identityProviders.ldap.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {saveError && <div className="error-banner">{saveError}</div>}
      {saved && <div className="helper-text" style={{ color: "var(--success)", marginBottom: 12 }}>{t("settings.identityProviders.ldap.saved")}</div>}

      <div className="form-grid">
        <div className="field">
          <label htmlFor="ldap-host">{t("settings.identityProviders.ldap.host")}</label>
          <input id="ldap-host" className="input" value={host} onChange={(e) => setHost(e.target.value)} placeholder="ldap.suaempresa.local" required />
        </div>
        <div className="field">
          <label htmlFor="ldap-port">{t("settings.identityProviders.ldap.port")}</label>
          <input id="ldap-port" className="input" type="number" value={port} onChange={(e) => setPort(Number(e.target.value))} required />
        </div>
        <div className="field field-full">
          <label className="checkbox-row" style={{ textTransform: "none" }}>
            <input type="checkbox" checked={useTls} onChange={(e) => setUseTls(e.target.checked)} />
            {t("settings.identityProviders.ldap.useTls")}
          </label>
        </div>
        <div className="field">
          <label htmlFor="ldap-binddn">{t("settings.identityProviders.ldap.bindDn")}</label>
          <input id="ldap-binddn" className="input" value={bindDn} onChange={(e) => setBindDn(e.target.value)} placeholder="cn=argusops-svc,dc=acme,dc=local" required />
        </div>
        <div className="field">
          <label htmlFor="ldap-bindpw">
            {t("settings.identityProviders.ldap.bindPassword")}{" "}
            {configured && <span className="field-hint">{t("settings.identityProviders.ldap.keepCurrent")}</span>}
          </label>
          <input id="ldap-bindpw" className="input" type="password" value={bindPassword} onChange={(e) => setBindPassword(e.target.value)} required={!configured} />
        </div>
        <div className="field field-full">
          <label htmlFor="ldap-userbase">{t("settings.identityProviders.ldap.userBaseDn")}</label>
          <input id="ldap-userbase" className="input" value={userBaseDn} onChange={(e) => setUserBaseDn(e.target.value)} placeholder="ou=people,dc=acme,dc=local" required />
        </div>
        <div className="field">
          <label htmlFor="ldap-filter">
            {t("settings.identityProviders.ldap.userFilter")}{" "}
            <span className="field-hint">{t("settings.identityProviders.ldap.userFilterHint")}</span>
          </label>
          <input id="ldap-filter" className="input mono" value={userFilter} onChange={(e) => setUserFilter(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="ldap-groupattr">{t("settings.identityProviders.ldap.groupAttribute")}</label>
          <input id="ldap-groupattr" className="input" value={groupAttribute} onChange={(e) => setGroupAttribute(e.target.value)} required />
        </div>
        <div className="field field-full">
          <label htmlFor="ldap-groupbase">
            {t("settings.identityProviders.ldap.groupBaseDn")}{" "}
            <span className="field-hint">{t("settings.identityProviders.ldap.optional")}</span>
          </label>
          <input id="ldap-groupbase" className="input" value={groupBaseDn} onChange={(e) => setGroupBaseDn(e.target.value)} placeholder="ou=groups,dc=acme,dc=local" />
        </div>
      </div>

      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.saving") : configured ? t("common.update") : t("settings.identityProviders.ldap.configureButton")}
        </button>
        {configured && !confirmingRemove && (
          <button type="button" className="btn btn-danger btn-sm" onClick={() => confirmRemove()} disabled={submitting}>
            {t("settings.identityProviders.ldap.remove")}
          </button>
        )}
        {configured && confirmingRemove && (
          <>
            <span className="helper-text">{t("settings.identityProviders.ldap.removeConfirm")}</span>
            <button type="button" className="btn btn-danger btn-sm" onClick={handleRemove} disabled={submitting}>
              {submitting ? t("common.saving") : t("common.confirmDelete")}
            </button>
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => cancelRemove()} disabled={submitting}>
              {t("common.cancel")}
            </button>
          </>
        )}
      </div>
    </form>
  );
}

function SAMLPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: existing, loading, error, configured, saveError, submitting, saved, save } = useAdminSingletonConfig<SAMLConfig | null>(
    ["identity-provider-saml"],
    (tok) => api.get<SAMLConfig | null>("/api/v1/settings/identity-providers/saml", tok),
  );

  const [metadataMode, setMetadataMode] = useState<"url" | "xml">("url");
  const [idpMetadataUrl, setIdpMetadataUrl] = useState("");
  const [idpMetadataXml, setIdpMetadataXml] = useState("");
  const [spEntityId, setSpEntityId] = useState(() => `${window.location.origin}/auth/saml`);
  const [acsUrl, setAcsUrl] = useState(() => `${window.location.origin}/auth/saml/acs`);
  const [groupAttribute, setGroupAttribute] = useState("groups");
  // Inline confirm/cancel instead of window.confirm() -- some embedded
  // browser contexts silently auto-dismiss native confirm() dialogs, which
  // made delete look like it does nothing (see OnCallScheduleDetailPage/TagsPanel).
  const { confirming: confirmingRemove, confirm: confirmRemove, cancel: cancelRemove } = useConfirm();

  useEffect(() => {
    if (existing) {
      if (existing.idpMetadataUrl) {
        setMetadataMode("url");
        setIdpMetadataUrl(existing.idpMetadataUrl);
      } else if (existing.idpMetadataXml) {
        setMetadataMode("xml");
        setIdpMetadataXml(existing.idpMetadataXml);
      }
      setSpEntityId(existing.spEntityId);
      setAcsUrl(existing.acsUrl);
      if (existing.groupAttribute) setGroupAttribute(existing.groupAttribute);
    }
  }, [existing]);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    await save(async () => {
      await api.put(
        "/api/v1/settings/identity-providers/saml",
        {
          idpMetadataUrl: metadataMode === "url" ? idpMetadataUrl : undefined,
          idpMetadataXml: metadataMode === "xml" ? idpMetadataXml : undefined,
          acsUrl,
          spEntityId,
          groupAttribute,
        },
        token,
      );
    });
  }

  async function handleRemove() {
    cancelRemove();
    await save(
      async () => {
        await api.del("/api/v1/settings/identity-providers/saml", token);
        setMetadataMode("url");
        setIdpMetadataUrl("");
        setIdpMetadataXml("");
        setSpEntityId(`${window.location.origin}/auth/saml`);
        setAcsUrl(`${window.location.origin}/auth/saml/acs`);
        setGroupAttribute("groups");
      },
      { markSavedOnSuccess: false },
    );
  }

  if (loading) return <div className="panel"><div className="empty-state">{t("common.loading")}</div></div>;

  const metadataUrl = `${window.location.origin}/auth/saml/metadata`;
  const loginUrl = `${window.location.origin}/auth/saml/login`;

  return (
    <form onSubmit={handleSubmit} className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.identityProviders.saml.title")}</h2>
        {configured && (
          <span className="badge badge-success">
            <span className="badge-status-dot" />
            {t("settings.identityProviders.saml.configured")}
          </span>
        )}
      </div>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.identityProviders.saml.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {saveError && <div className="error-banner">{saveError}</div>}
      {saved && <div className="helper-text" style={{ color: "var(--success)", marginBottom: 12 }}>{t("settings.identityProviders.saml.saved")}</div>}

      {configured && (
        <div className="panel" style={{ background: "var(--surface-2)", marginBottom: 14 }}>
          <p className="row-title" style={{ marginBottom: 8 }}>{t("settings.identityProviders.saml.registerWithIdp")}</p>
          <div className="field" style={{ marginBottom: 8 }}>
            <label>{t("settings.identityProviders.saml.spMetadataUrl")}</label>
            <div className="token-reveal">
              <code style={{ fontSize: 11.5, wordBreak: "break-all" }}>{metadataUrl}</code>
              <button type="button" className="btn btn-sm" onClick={() => navigator.clipboard.writeText(metadataUrl)}>
                {t("common.copy")}
              </button>
            </div>
          </div>
          <div className="field" style={{ marginBottom: 0 }}>
            <label>{t("settings.identityProviders.saml.loginUrlLabel")}</label>
            <div className="token-reveal">
              <code style={{ fontSize: 11.5, wordBreak: "break-all" }}>{loginUrl}</code>
              <button type="button" className="btn btn-sm" onClick={() => navigator.clipboard.writeText(loginUrl)}>
                {t("common.copy")}
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="field">
        <label>{t("settings.identityProviders.saml.metadataMode")}</label>
        <div className="pill-tabs">
          <button type="button" className="pill-tab" data-active={metadataMode === "url"} onClick={() => setMetadataMode("url")}>
            {t("settings.identityProviders.saml.urlTab")}
          </button>
          <button type="button" className="pill-tab" data-active={metadataMode === "xml"} onClick={() => setMetadataMode("xml")}>
            {t("settings.identityProviders.saml.xmlTab")}
          </button>
        </div>
      </div>

      {metadataMode === "url" ? (
        <div className="field">
          <label htmlFor="saml-idpurl">{t("settings.identityProviders.saml.idpMetadataUrl")}</label>
          <input
            id="saml-idpurl"
            className="input"
            placeholder="https://idp.suaempresa.com/metadata"
            value={idpMetadataUrl}
            onChange={(e) => setIdpMetadataUrl(e.target.value)}
            required={metadataMode === "url"}
          />
        </div>
      ) : (
        <div className="field">
          <label htmlFor="saml-idpxml">{t("settings.identityProviders.saml.idpMetadataXml")}</label>
          <textarea
            id="saml-idpxml"
            className="textarea mono"
            style={{ minHeight: 120 }}
            value={idpMetadataXml}
            onChange={(e) => setIdpMetadataXml(e.target.value)}
            required={metadataMode === "xml"}
          />
        </div>
      )}

      <div className="form-grid">
        <div className="field">
          <label htmlFor="saml-entityid">{t("settings.identityProviders.saml.spEntityId")}</label>
          <input id="saml-entityid" className="input" value={spEntityId} onChange={(e) => setSpEntityId(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="saml-acs">{t("settings.identityProviders.saml.acsUrl")}</label>
          <input id="saml-acs" className="input" value={acsUrl} onChange={(e) => setAcsUrl(e.target.value)} required />
        </div>
        <div className="field field-full">
          <label htmlFor="saml-groupattr">
            {t("settings.identityProviders.saml.groupAttribute")}{" "}
            <span className="field-hint">{t("settings.identityProviders.saml.groupAttributeHint")}</span>
          </label>
          <input id="saml-groupattr" className="input" value={groupAttribute} onChange={(e) => setGroupAttribute(e.target.value)} />
        </div>
      </div>

      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.saving") : configured ? t("common.update") : t("settings.identityProviders.saml.configureButton")}
        </button>
        {configured && !confirmingRemove && (
          <button type="button" className="btn btn-danger btn-sm" onClick={() => confirmRemove()} disabled={submitting}>
            {t("settings.identityProviders.saml.remove")}
          </button>
        )}
        {configured && confirmingRemove && (
          <>
            <span className="helper-text">{t("settings.identityProviders.saml.removeConfirm")}</span>
            <button type="button" className="btn btn-danger btn-sm" onClick={handleRemove} disabled={submitting}>
              {submitting ? t("common.saving") : t("common.confirmDelete")}
            </button>
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => cancelRemove()} disabled={submitting}>
              {t("common.cancel")}
            </button>
          </>
        )}
      </div>
    </form>
  );
}
