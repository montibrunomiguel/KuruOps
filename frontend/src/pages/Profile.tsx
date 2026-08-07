import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";

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
