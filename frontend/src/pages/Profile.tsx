import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { useList, mutationErrorMessage } from "../api/hooks";
import type { UserAPIToken } from "../types/api";
import { formatDateTime } from "../lib/format";

// Self-service "my account" page -- name/email (email change requires
// current-password confirmation, mirroring AuthService.UpdateProfile's
// guard) plus the same change-password flow ChangePasswordPage uses,
// embedded as a sub-section here for accounts that aren't currently forced
// through the full-screen version.
export function ProfilePage() {
  const { t } = useTranslation();

  return (
    <>
      <Link to="/dashboard" className="back-link">
        ‹ {t("profile.backToDashboard")}
      </Link>
      <h1 className="page-title">{t("profile.title")}</h1>
      <p className="page-sub">{t("profile.subtitle")}</p>
      <ProfileForm />
      <PasswordSection />
      <APITokensSection />
    </>
  );
}

function ProfileForm() {
  const { t } = useTranslation();
  const { token, user, updateProfile } = useAuth();
  const [name, setName] = useState(user?.name ?? "");
  const [email, setEmail] = useState(user?.email ?? "");
  const [currentPassword, setCurrentPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const emailChanged = email !== user?.email;

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    setSaved(false);
    try {
      await api.put(
        "/api/v1/account/profile",
        { name, email, currentPassword: emailChanged ? currentPassword : undefined },
        token,
      );
      updateProfile(name, email);
      setCurrentPassword("");
      setSaved(true);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 16 }}>
      <h2 className="panel-title" style={{ marginBottom: 14 }}>
        {t("profile.detailsTitle")}
      </h2>

      {error && <div className="error-banner">{error}</div>}
      {saved && <div className="helper-text" style={{ color: "var(--success)", marginBottom: 12 }}>{t("profile.saved")}</div>}

      <div className="form-grid">
        <div className="field">
          <label htmlFor="profile-name">{t("profile.name")}</label>
          <input id="profile-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="profile-email">{t("profile.email")}</label>
          <input
            id="profile-email"
            className="input"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
        </div>
        {emailChanged && (
          <div className="field">
            <label htmlFor="profile-current-password">
              {t("profile.currentPassword")} <span className="field-hint">{t("profile.currentPasswordHint")}</span>
            </label>
            <input
              id="profile-current-password"
              className="input"
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              required
            />
          </div>
        )}
      </div>

      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
      </div>
    </form>
  );
}

function PasswordSection() {
  const { t } = useTranslation();
  const { token, applyNewToken } = useAuth();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSaved(false);
    if (newPassword !== confirmPassword) {
      setError(t("changePassword.mismatch"));
      return;
    }
    if (newPassword.length < 8) {
      setError(t("changePassword.tooShort"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await api.post<{ token: string }>(
        "/api/v1/account/change-password",
        { currentPassword, newPassword },
        token,
      );
      applyNewToken(res.token);
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      setSaved(true);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel">
      <h2 className="panel-title" style={{ marginBottom: 14 }}>
        {t("profile.passwordTitle")}
      </h2>

      {error && <div className="error-banner">{error}</div>}
      {saved && <div className="helper-text" style={{ color: "var(--success)", marginBottom: 12 }}>{t("profile.saved")}</div>}

      <div className="form-grid">
        <div className="field">
          <label htmlFor="profile-current-pw">{t("changePassword.currentPassword")}</label>
          <input
            id="profile-current-pw"
            className="input"
            type="password"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="profile-new-pw">{t("changePassword.newPassword")}</label>
          <input
            id="profile-new-pw"
            className="input"
            type="password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            minLength={8}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="profile-confirm-pw">{t("changePassword.confirmPassword")}</label>
          <input
            id="profile-confirm-pw"
            className="input"
            type="password"
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            minLength={8}
            required
          />
        </div>
      </div>

      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("changePassword.submitting") : t("changePassword.submit")}
        </button>
      </div>
    </form>
  );
}

function apiTokenExpiryOptions(t: (k: string) => string): { value: string; label: string }[] {
  return [
    { value: "30", label: t("settings.webhooks.expiry.30") },
    { value: "90", label: t("settings.webhooks.expiry.90") },
    { value: "180", label: t("settings.webhooks.expiry.180") },
    { value: "365", label: t("settings.webhooks.expiry.365") },
    { value: "never", label: t("settings.webhooks.expiry.never") },
  ];
}

function apiTokenExpiryToDays(value: string): number | undefined {
  // undefined -> let the backend apply its own default (90d); "never" -> 0,
  // which service.resolveExpiry treats as an explicit opt-out. Reuses the
  // webhook expiry copy (settings.webhooks.expiry.*) rather than adding a
  // near-identical i18n block just for this second, unrelated use of the
  // exact same "30/90/180/365/never" choice set.
  if (value === "never") return 0;
  return Number(value);
}

// Self-service personal API tokens -- an alternative to a JWT session for
// scripted/API access. Same one-time-plaintext-display + list + revoke
// pattern as Settings -> Webhook Endpoints' WebhooksPanel (admin-only,
// different resource), just for a token that authenticates AS this user
// with their own current permissions rather than a tenant-wide webhook.
function APITokensSection() {
  const { t } = useTranslation();
  const { data: tokens, loading, error, reload } = useList<UserAPIToken>(
    (tk) => api.get<UserAPIToken[]>("/api/v1/account/api-tokens", tk),
  );

  const [showCreate, setShowCreate] = useState(false);
  const [newToken, setNewToken] = useState<{ name: string; plaintext: string } | null>(null);

  const activeTokens = (tokens ?? []).filter((tok) => !tok.revokedAt);

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("profile.apiTokens.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("profile.apiTokens.newToken")}
        </button>
      </div>
      <p className="helper-text" style={{ marginBottom: 14 }}>{t("profile.apiTokens.hint")}</p>

      {error && <div className="error-banner">{error}</div>}

      {newToken && (
        <div className="panel" style={{ background: "var(--accent-soft)", borderColor: "var(--accent)", marginBottom: 14 }}>
          <p style={{ margin: "0 0 8px", fontSize: 12.5 }}>
            {t("profile.apiTokens.tokenGenerated", { name: newToken.name })}
          </p>
          <div className="token-reveal">
            <code style={{ fontSize: 12 }}>{newToken.plaintext}</code>
            <button className="btn btn-sm" onClick={() => navigator.clipboard.writeText(newToken.plaintext)}>
              {t("common.copy")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setNewToken(null)}>
              {t("common.close")}
            </button>
          </div>
        </div>
      )}

      {showCreate && (
        <CreateAPITokenForm
          onCancel={() => setShowCreate(false)}
          onCreated={(name, plaintext) => {
            setShowCreate(false);
            setNewToken({ name, plaintext });
            reload();
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && activeTokens.length === 0 && <div className="empty-state">{t("profile.apiTokens.noTokens")}</div>}

      {!loading &&
        activeTokens.map((tok) => <APITokenRow key={tok.id} apiToken={tok} onRevoked={reload} />)}
    </div>
  );
}

function CreateAPITokenForm({
  onCancel,
  onCreated,
}: {
  onCancel: () => void;
  onCreated: (name: string, plaintext: string) => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState("");
  const [expiresInDays, setExpiresInDays] = useState("90");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const res = await api.post<{ token: UserAPIToken; plaintext: string }>(
        "/api/v1/account/api-tokens",
        { name, expiresInDays: apiTokenExpiryToDays(expiresInDays) },
        token,
      );
      onCreated(name, res.plaintext);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  const options = apiTokenExpiryOptions(t);

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 14 }}>
      {error && <div className="error-banner">{error}</div>}
      <div className="form-grid">
        <div className="field">
          <label htmlFor="pat-name">{t("profile.apiTokens.form.name")}</label>
          <input
            id="pat-name"
            className="input"
            placeholder={t("profile.apiTokens.form.namePlaceholder")}
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="pat-expiry">{t("settings.webhooks.form.tokenExpiry")}</label>
          <select id="pat-expiry" className="select" value={expiresInDays} onChange={(e) => setExpiresInDays(e.target.value)}>
            {options.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.creating") : t("common.create")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}

function APITokenRow({ apiToken, onRevoked }: { apiToken: UserAPIToken; onRevoked: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function revoke() {
    setBusy(true);
    setError(null);
    try {
      await api.del(`/api/v1/account/api-tokens/${apiToken.id}`, token);
      onRevoked();
    } catch (err) {
      setError(mutationErrorMessage(err));
      setBusy(false);
    }
  }

  return (
    <div className="row">
      <div className="row-main">
        <p className="row-title">{apiToken.name}</p>
        <p className="row-sub">
          pat_••••••••{apiToken.tokenLast4} ·{" "}
          {apiToken.expiresAt
            ? t("profile.apiTokens.expiresOn", { date: formatDateTime(apiToken.expiresAt) })
            : t("settings.webhooks.badge.neverExpires")}
        </p>
        {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
      </div>
      <div className="row-actions">
        <button className="btn btn-sm" onClick={revoke} disabled={busy}>
          {t("profile.apiTokens.revoke")}
        </button>
      </div>
    </div>
  );
}
