import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { mutationErrorMessage, useObject } from "../../api/hooks";
import { useConfirm } from "../../hooks/useConfirm";
import type { StorageConfig } from "../../types/api";

// Settings -> Storage Integration: lets an admin point alert/incident
// evidence uploads at an S3, GCS, or Google Drive destination instead of
// the API container's local disk (see backend handlers.UploadHandlers).
// Same shape as IdentityProvidersPanel's LDAP/SAML forms -- a write-only
// secret field (access key / credentials JSON) that stays blank on load
// and is only re-sent if the admin types a new value, and a "configured"
// badge derived from the fetched config being non-null rather than a
// separate flag.
//
// Google Drive is the one provider with a choice of two auth methods --
// paste a service-account JSON key (same UX as GCS), or "Connect your
// Google account" (an OAuth redirect: this page navigates away entirely,
// Google redirects to the backend's callback, which redirects back here
// with ?gdrive_connected=1 or ?gdrive_error=... -- there's no XHR request/
// response for that half of the flow, only the query-param round trip).
export function StorageIntegrationPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: existing, loading, error, reload } = useObject<StorageConfig | null>(["storage-config"], (tok) =>
    api.get<StorageConfig | null>("/api/v1/settings/storage", tok),
  );
  const [searchParams, setSearchParams] = useSearchParams();

  const [provider, setProvider] = useState<"s3" | "gcs" | "gdrive">("s3");

  const [s3Bucket, setS3Bucket] = useState("");
  const [s3Region, setS3Region] = useState("");
  const [s3AccessKeyId, setS3AccessKeyId] = useState("");
  const [s3SecretAccessKey, setS3SecretAccessKey] = useState("");

  const [gcsBucket, setGcsBucket] = useState("");
  const [gcsProjectId, setGcsProjectId] = useState("");
  const [gcsCredentialsJson, setGcsCredentialsJson] = useState("");

  const [gdriveAuthMethod, setGdriveAuthMethod] = useState<"service_account" | "oauth">("service_account");
  const [gdriveFolderId, setGdriveFolderId] = useState("");
  const [gdriveServiceAccountJson, setGdriveServiceAccountJson] = useState("");
  const [connectingGoogle, setConnectingGoogle] = useState(false);

  const [submitting, setSubmitting] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(searchParams.get("gdrive_error"));
  const [saved, setSaved] = useState(searchParams.get("gdrive_connected") === "1");
  // Inline confirm/cancel instead of window.confirm() -- some embedded
  // browser contexts silently auto-dismiss native confirm() dialogs, which
  // made delete look like it does nothing (see OnCallScheduleDetailPage/TagsPanel).
  const { confirming: confirmingRemove, confirm: confirmRemove, cancel: cancelRemove } = useConfirm();

  // Clears gdrive_connected/gdrive_error from the URL once shown, so a
  // page refresh doesn't keep re-displaying a stale result from the OAuth
  // redirect.
  useEffect(() => {
    if (searchParams.has("gdrive_connected") || searchParams.has("gdrive_error")) {
      setSearchParams({}, { replace: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (existing) {
      setProvider(existing.provider);
      if (existing.provider === "s3") {
        setS3Bucket(existing.s3Bucket ?? "");
        setS3Region(existing.s3Region ?? "");
        setS3AccessKeyId(existing.s3AccessKeyId ?? "");
      } else if (existing.provider === "gcs") {
        setGcsBucket(existing.gcsBucket ?? "");
        setGcsProjectId(existing.gcsProjectId ?? "");
      } else if (existing.provider === "gdrive") {
        setGdriveFolderId(existing.gdriveFolderId ?? "");
        setGdriveAuthMethod(existing.gdriveAuthMethod ?? "service_account");
      }
    }
  }, [existing]);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setSaveError(null);
    setSaved(false);
    try {
      if (provider === "s3") {
        await api.put(
          "/api/v1/settings/storage/s3",
          { bucket: s3Bucket, region: s3Region, accessKeyId: s3AccessKeyId, secretAccessKey: s3SecretAccessKey },
          token,
        );
        setS3SecretAccessKey("");
      } else if (provider === "gcs") {
        await api.put(
          "/api/v1/settings/storage/gcs",
          { bucket: gcsBucket, projectId: gcsProjectId, credentialsJson: gcsCredentialsJson },
          token,
        );
        setGcsCredentialsJson("");
      } else if (gdriveAuthMethod === "service_account") {
        await api.put(
          "/api/v1/settings/storage/gdrive/service-account",
          { folderId: gdriveFolderId, serviceAccountJson: gdriveServiceAccountJson },
          token,
        );
        setGdriveServiceAccountJson("");
      } else {
        // OAuth: no PUT here -- redirect the whole page to Google's consent
        // screen. Success/failure comes back as a query param on this same
        // page (?gdrive_connected=1 / ?gdrive_error=...), not a response to
        // this request.
        if (!gdriveFolderId) throw new Error(t("settings.storage.gdrive.folderIdRequired"));
        setConnectingGoogle(true);
        const { url } = await api.get<{ url: string }>(
          `/api/v1/settings/storage/gdrive/oauth/authorize-url?folderId=${encodeURIComponent(gdriveFolderId)}`,
          token,
        );
        window.location.href = url;
        return;
      }
      setSaved(true);
      reload();
    } catch (err) {
      setSaveError(mutationErrorMessage(err));
      setConnectingGoogle(false);
    } finally {
      setSubmitting(false);
    }
  }

  async function handleRemove() {
    cancelRemove();
    setSubmitting(true);
    try {
      await api.del("/api/v1/settings/storage", token);
      setS3Bucket("");
      setS3Region("");
      setS3AccessKeyId("");
      setGcsBucket("");
      setGcsProjectId("");
      setGdriveFolderId("");
      reload();
    } catch (err) {
      setSaveError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  if (loading) return <div className="panel"><div className="empty-state">{t("common.loading")}</div></div>;

  return (
    <form onSubmit={handleSubmit} className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.storage.title")}</h2>
        {existing && (
          <span className="badge badge-success">
            <span className="badge-status-dot" />
            {existing.provider === "gdrive" && existing.gdriveAuthMethod === "oauth" && existing.gdriveOauthConnectedEmail
              ? t("settings.storage.gdrive.connectedAs", { email: existing.gdriveOauthConnectedEmail })
              : t("settings.storage.configured", { provider: existing.provider.toUpperCase() })}
          </span>
        )}
      </div>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.storage.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {saveError && <div className="error-banner">{saveError}</div>}
      {saved && <div className="helper-text" style={{ color: "var(--success)", marginBottom: 12 }}>{t("settings.storage.saved")}</div>}

      <div className="field">
        <label>{t("settings.storage.provider")}</label>
        <div className="pill-tabs">
          <button type="button" className="pill-tab" data-active={provider === "s3"} onClick={() => setProvider("s3")}>
            Amazon S3
          </button>
          <button type="button" className="pill-tab" data-active={provider === "gcs"} onClick={() => setProvider("gcs")}>
            Google Cloud Storage
          </button>
          <button type="button" className="pill-tab" data-active={provider === "gdrive"} onClick={() => setProvider("gdrive")}>
            Google Drive
          </button>
        </div>
      </div>

      {provider === "s3" && (
        <div className="form-grid">
          <div className="field">
            <label htmlFor="storage-s3-bucket">{t("settings.storage.s3.bucket")}</label>
            <input id="storage-s3-bucket" className="input" value={s3Bucket} onChange={(e) => setS3Bucket(e.target.value)} required />
          </div>
          <div className="field">
            <label htmlFor="storage-s3-region">{t("settings.storage.s3.region")}</label>
            <input id="storage-s3-region" className="input" placeholder="us-east-1" value={s3Region} onChange={(e) => setS3Region(e.target.value)} required />
          </div>
          <div className="field">
            <label htmlFor="storage-s3-key">{t("settings.storage.s3.accessKeyId")}</label>
            <input id="storage-s3-key" className="input" value={s3AccessKeyId} onChange={(e) => setS3AccessKeyId(e.target.value)} required />
          </div>
          <div className="field">
            <label htmlFor="storage-s3-secret">
              {t("settings.storage.s3.secretAccessKey")}{" "}
              {existing?.provider === "s3" && <span className="field-hint">{t("settings.storage.keepCurrent")}</span>}
            </label>
            <input
              id="storage-s3-secret"
              className="input"
              type="password"
              value={s3SecretAccessKey}
              onChange={(e) => setS3SecretAccessKey(e.target.value)}
              required={existing?.provider !== "s3"}
            />
          </div>
        </div>
      )}

      {provider === "gcs" && (
        <div className="form-grid">
          <div className="field">
            <label htmlFor="storage-gcs-bucket">{t("settings.storage.gcs.bucket")}</label>
            <input id="storage-gcs-bucket" className="input" value={gcsBucket} onChange={(e) => setGcsBucket(e.target.value)} required />
          </div>
          <div className="field">
            <label htmlFor="storage-gcs-project">{t("settings.storage.gcs.projectId")}</label>
            <input id="storage-gcs-project" className="input" value={gcsProjectId} onChange={(e) => setGcsProjectId(e.target.value)} required />
          </div>
          <div className="field field-full">
            <label htmlFor="storage-gcs-creds">
              {t("settings.storage.gcs.credentialsJson")}{" "}
              {existing?.provider === "gcs" && <span className="field-hint">{t("settings.storage.keepCurrent")}</span>}
            </label>
            <textarea
              id="storage-gcs-creds"
              className="textarea mono"
              style={{ minHeight: 100 }}
              value={gcsCredentialsJson}
              onChange={(e) => setGcsCredentialsJson(e.target.value)}
              required={existing?.provider !== "gcs"}
            />
          </div>
        </div>
      )}

      {provider === "gdrive" && (
        <>
          <div className="field">
            <label>{t("settings.storage.gdrive.authMethod")}</label>
            <div className="pill-tabs">
              <button
                type="button"
                className="pill-tab"
                data-active={gdriveAuthMethod === "service_account"}
                onClick={() => setGdriveAuthMethod("service_account")}
              >
                {t("settings.storage.gdrive.serviceAccount")}
              </button>
              <button type="button" className="pill-tab" data-active={gdriveAuthMethod === "oauth"} onClick={() => setGdriveAuthMethod("oauth")}>
                {t("settings.storage.gdrive.connectGoogleAccount")}
              </button>
            </div>
          </div>

          <div className="form-grid">
            <div className="field">
              <label htmlFor="storage-gdrive-folder">{t("settings.storage.gdrive.folderId")}</label>
              <input
                id="storage-gdrive-folder"
                className="input"
                value={gdriveFolderId}
                onChange={(e) => setGdriveFolderId(e.target.value)}
                required
              />
              <span className="field-hint">{t("settings.storage.gdrive.folderIdHint")}</span>
            </div>

            {gdriveAuthMethod === "service_account" ? (
              <div className="field field-full">
                <label htmlFor="storage-gdrive-creds">
                  {t("settings.storage.gdrive.serviceAccountJson")}{" "}
                  {existing?.provider === "gdrive" && existing.gdriveAuthMethod === "service_account" && (
                    <span className="field-hint">{t("settings.storage.keepCurrent")}</span>
                  )}
                </label>
                <textarea
                  id="storage-gdrive-creds"
                  className="textarea mono"
                  style={{ minHeight: 100 }}
                  value={gdriveServiceAccountJson}
                  onChange={(e) => setGdriveServiceAccountJson(e.target.value)}
                  required={!(existing?.provider === "gdrive" && existing.gdriveAuthMethod === "service_account")}
                />
                <span className="field-hint">{t("settings.storage.gdrive.serviceAccountHint")}</span>
              </div>
            ) : (
              <div className="field field-full helper-text">{t("settings.storage.gdrive.oauthHint")}</div>
            )}
          </div>
        </>
      )}

      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting || connectingGoogle}>
          {provider === "gdrive" && gdriveAuthMethod === "oauth"
            ? connectingGoogle
              ? t("common.saving")
              : t("settings.storage.gdrive.connectButton")
            : submitting
              ? t("common.saving")
              : existing
                ? t("common.update")
                : t("settings.storage.configureButton")}
        </button>
        {existing && !confirmingRemove && (
          <button type="button" className="btn btn-danger btn-sm" onClick={() => confirmRemove()} disabled={submitting}>
            {t("settings.storage.remove")}
          </button>
        )}
        {existing && confirmingRemove && (
          <>
            <span className="helper-text">{t("settings.storage.removeConfirm")}</span>
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
