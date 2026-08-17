import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { mutationErrorMessage, useObject } from "../../api/hooks";
import type { SMTPConfig } from "../../types/api";
import { useConfirm } from "../../hooks/useConfirm";

// Settings -> SMTP: lets an admin point outbound transactional email
// (password reset, and any future notification) at a real relay. Same
// shape as StorageIntegrationPanel -- a write-only secret field (password)
// that stays blank on load and is only re-sent if the admin types a new
// value, plus a "Send test email" action to confirm it actually works.
export function SMTPConfigPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: existing, loading, error, reload } = useObject<SMTPConfig | null>(["smtp-config"], (tok) =>
    api.get<SMTPConfig | null>("/api/v1/settings/smtp", tok),
  );

  const [host, setHost] = useState("");
  const [port, setPort] = useState("587");
  const [useTls, setUseTls] = useState(true);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [fromAddress, setFromAddress] = useState("");
  const [fromName, setFromName] = useState("");

  const [submitting, setSubmitting] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  // Inline confirm/cancel instead of window.confirm() -- some embedded
  // browser contexts silently auto-dismiss native confirm() dialogs, which
  // made delete look like it does nothing (see OnCallScheduleDetailPage/TagsPanel).
  const { confirming: confirmingRemove, confirm: confirmRemove, cancel: cancelRemove } = useConfirm();

  const [testTo, setTestTo] = useState("");
  const [testSending, setTestSending] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null);

  useEffect(() => {
    if (existing) {
      setHost(existing.host);
      setPort(String(existing.port));
      setUseTls(existing.useTls);
      setUsername(existing.username);
      setFromAddress(existing.fromAddress);
      setFromName(existing.fromName ?? "");
    }
  }, [existing]);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setSaveError(null);
    setSaved(false);
    try {
      await api.put(
        "/api/v1/settings/smtp",
        { host, port: Number(port), useTls, username, password, fromAddress, fromName },
        token,
      );
      setPassword("");
      setSaved(true);
      reload();
    } catch (err) {
      setSaveError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleRemove() {
    cancelRemove();
    setSubmitting(true);
    try {
      await api.del("/api/v1/settings/smtp", token);
      setHost("");
      setPort("587");
      setUseTls(true);
      setUsername("");
      setFromAddress("");
      setFromName("");
      reload();
    } catch (err) {
      setSaveError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleSendTest(e: FormEvent) {
    e.preventDefault();
    setTestSending(true);
    setTestResult(null);
    try {
      await api.post("/api/v1/settings/smtp/test", { to: testTo }, token);
      setTestResult({ ok: true, message: t("settings.smtp.testSent", { to: testTo }) });
    } catch (err) {
      setTestResult({ ok: false, message: t("settings.smtp.testFailed", { error: mutationErrorMessage(err) }) });
    } finally {
      setTestSending(false);
    }
  }

  if (loading) return <div className="panel"><div className="empty-state">{t("common.loading")}</div></div>;

  return (
    <>
      <form onSubmit={handleSubmit} className="panel">
        <div className="panel-header">
          <h2 className="panel-title">{t("settings.smtp.title")}</h2>
          {existing && (
            <span className="badge badge-success">
              <span className="badge-status-dot" />
              {t("settings.smtp.configured")}
            </span>
          )}
        </div>
        <p className="helper-text" style={{ marginBottom: 14 }}>
          {t("settings.smtp.helper")}
        </p>

        {error && <div className="error-banner">{error}</div>}
        {saveError && <div className="error-banner">{saveError}</div>}
        {saved && <div className="helper-text" style={{ color: "var(--success)", marginBottom: 12 }}>{t("settings.smtp.saved")}</div>}

        <div className="form-grid">
          <div className="field">
            <label htmlFor="smtp-host">{t("settings.smtp.host")}</label>
            <input id="smtp-host" className="input" value={host} onChange={(e) => setHost(e.target.value)} required />
          </div>
          <div className="field">
            <label htmlFor="smtp-port">{t("settings.smtp.port")}</label>
            <input id="smtp-port" className="input" type="number" value={port} onChange={(e) => setPort(e.target.value)} required />
          </div>
          <div className="field">
            <label htmlFor="smtp-username">
              {t("settings.smtp.username")} <span className="field-hint">{t("settings.smtp.usernameHint")}</span>
            </label>
            <input id="smtp-username" className="input" value={username} onChange={(e) => setUsername(e.target.value)} />
          </div>
          <div className="field">
            <label htmlFor="smtp-password">
              {t("settings.smtp.password")}{" "}
              {existing && <span className="field-hint">{t("settings.smtp.keepCurrent")}</span>}
            </label>
            <input id="smtp-password" className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
          <div className="field">
            <label htmlFor="smtp-from-address">{t("settings.smtp.fromAddress")}</label>
            <input id="smtp-from-address" className="input" type="email" value={fromAddress} onChange={(e) => setFromAddress(e.target.value)} required />
          </div>
          <div className="field">
            <label htmlFor="smtp-from-name">
              {t("settings.smtp.fromName")} <span className="field-hint">{t("settings.smtp.fromNameHint")}</span>
            </label>
            <input id="smtp-from-name" className="input" value={fromName} onChange={(e) => setFromName(e.target.value)} />
          </div>
          <div className="field">
            <label className="checkbox-label">
              <input type="checkbox" checked={useTls} onChange={(e) => setUseTls(e.target.checked)} />
              {t("settings.smtp.useTls")}
            </label>
          </div>
        </div>

        <div className="row-actions">
          <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
            {submitting ? t("common.saving") : existing ? t("common.update") : t("settings.smtp.configureButton")}
          </button>
          {existing && !confirmingRemove && (
            <button type="button" className="btn btn-danger btn-sm" onClick={() => confirmRemove()} disabled={submitting}>
              {t("settings.smtp.remove")}
            </button>
          )}
          {existing && confirmingRemove && (
            <>
              <span className="helper-text">{t("settings.smtp.removeConfirm")}</span>
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

      {existing && (
        <form onSubmit={handleSendTest} className="panel" style={{ marginTop: 16 }}>
          <h2 className="panel-title">{t("settings.smtp.sendTest")}</h2>
          {testResult && (
            <div className={testResult.ok ? "helper-text" : "error-banner"} style={testResult.ok ? { color: "var(--success)", marginBottom: 12 } : undefined}>
              {testResult.message}
            </div>
          )}
          <div className="row-actions">
            <input
              className="input"
              type="email"
              placeholder={t("settings.smtp.testEmailTo") ?? undefined}
              value={testTo}
              onChange={(e) => setTestTo(e.target.value)}
              required
            />
            <button type="submit" className="btn btn-secondary btn-sm" disabled={testSending}>
              {testSending ? t("settings.smtp.sendingTest") : t("settings.smtp.sendTest")}
            </button>
          </div>
        </form>
      )}
    </>
  );
}
