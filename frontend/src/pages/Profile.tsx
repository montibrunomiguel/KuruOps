import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { useList, mutationErrorMessage } from "../api/hooks";
import type { PersonalAccessToken } from "../types/api";
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
      <ApiTokensSection />
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

function tokenExpiryOptions(t: (k: string) => string): { value: string; label: string }[] {
  return [
    { value: "never", label: t("profile.tokens.expiry.never") },
    { value: "30", label: t("profile.tokens.expiry.30") },
    { value: "90", label: t("profile.tokens.expiry.90") },
    { value: "365", label: t("profile.tokens.expiry.365") },
  ];
}

function tokenExpiryToDays(value: string): number | undefined {
  // "never" -> 0, which the backend's resolvePATExpiry treats as an
  // explicit "no expiry" -- see PersonalAccessTokenService.Create's doc
  // comment for why that's this feature's default, unlike webhook tokens.
  if (value === "never") return 0;
  return Number(value);
}

// Settings -> My Account -- API Tokens: lets the signed-in user mint their
// own bearer credential for scripted access to the API (see
// middleware.APIAuth), independent of the per-endpoint webhook tokens in
// Settings -> Webhook Endpoints (those authenticate inbound alerts, not a
// person calling the API as themselves).
function ApiTokensSection() {
  const { t } = useTranslation();
  const { data: tokens, loading, error, reload } = useList<PersonalAccessToken>(
    (tk) => api.get<PersonalAccessToken[]>("/api/v1/account/tokens", tk),
  );

  const [showCreate, setShowCreate] = useState(false);
  const [revealed, setRevealed] = useState<{ name: string; plaintext: string } | null>(null);
  const tokenList = Array.isArray(tokens) ? tokens : [];

  return (
    <div className="panel" style={{ marginTop: 16 }}>
      <div className="panel-header">
        <h2 className="panel-title">{t("profile.tokens.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("profile.tokens.newToken")}
        </button>
      </div>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("profile.tokens.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}

      {revealed && (
        <div className="panel" style={{ background: "var(--accent-soft)", borderColor: "var(--accent)", marginBottom: 14 }}>
          <p style={{ margin: "0 0 8px", fontSize: 12.5 }}>
            {t("profile.tokens.tokenGenerated", { name: revealed.name })}
          </p>
          <div className="token-reveal">
            <code style={{ fontSize: 12 }}>{revealed.plaintext}</code>
            <button className="btn btn-sm" onClick={() => navigator.clipboard.writeText(revealed.plaintext)}>
              {t("common.copy")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setRevealed(null)}>
              {t("common.close")}
            </button>
          </div>
        </div>
      )}

      {showCreate && (
        <CreateTokenForm
          onCancel={() => setShowCreate(false)}
          onCreated={(name, plaintext) => {
            setShowCreate(false);
            setRevealed({ name, plaintext });
            reload();
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && tokenList.length === 0 && <div className="empty-state">{t("profile.tokens.empty")}</div>}
      {!loading && tokenList.map((tok) => <TokenRow key={tok.id} tokenItem={tok} onRevoked={reload} />)}
    </div>
  );
}

function CreateTokenForm({
  onCancel,
  onCreated,
}: {
  onCancel: () => void;
  onCreated: (name: string, plaintext: string) => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState("");
  const [expiresInDays, setExpiresInDays] = useState("never");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const res = await api.post<{ token: PersonalAccessToken; plaintext: string }>(
        "/api/v1/account/tokens",
        { name, expiresInDays: tokenExpiryToDays(expiresInDays) },
        token,
      );
      onCreated(name, res.plaintext);
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
          <label htmlFor="pat-name">{t("profile.tokens.form.name")}</label>
          <input
            id="pat-name"
            className="input"
            placeholder={t("profile.tokens.form.namePlaceholder")}
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="pat-expiry">{t("profile.tokens.form.expiry")}</label>
          <select id="pat-expiry" className="select" value={expiresInDays} onChange={(e) => setExpiresInDays(e.target.value)}>
            {tokenExpiryOptions(t).map((o) => (
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

function TokenRow({ tokenItem, onRevoked }: { tokenItem: PersonalAccessToken; onRevoked: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const revoked = !!tokenItem.revokedAt;

  async function revoke() {
    setBusy(true);
    setError(null);
    try {
      await api.del(`/api/v1/account/tokens/${tokenItem.id}`, token);
      onRevoked();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="row">
      <div className="row-main">
        <p className="row-title">
          {tokenItem.name}{" "}
          {revoked && <span className="badge badge-muted">{t("profile.tokens.revokedBadge")}</span>}
        </p>
        <p className="row-sub">
          pat_••••••••{tokenItem.tokenLast4} ·{" "}
          {tokenItem.expiresAt
            ? t("profile.tokens.expiresOn", { date: formatDateTime(tokenItem.expiresAt) })
            : t("profile.tokens.neverExpires")}
          {tokenItem.lastUsedAt
            ? ` · ${t("profile.tokens.lastUsed", { date: formatDateTime(tokenItem.lastUsedAt) })}`
            : ` · ${t("profile.tokens.neverUsed")}`}
        </p>
        {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
      </div>
      {!revoked && (
        <div className="row-actions">
          <button className="btn btn-sm" onClick={revoke} disabled={busy}>
            {t("profile.tokens.revoke")}
          </button>
        </div>
      )}
    </div>
  );
}
