import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";

// Shown whenever the session's mustChangePassword flag is set -- always
// true right after a fresh deploy's first login (see
// db/migrations/0013_seed_default_admin.up.sql), and for any account an
// admin has otherwise flagged. App.tsx routes everything here until it's
// cleared; there is no way to reach the rest of the app around this screen
// while the flag is set, since the backend itself rejects every other
// /api/v1 route in that state (middleware.RequirePasswordChanged).
export function ChangePasswordPage() {
  const { t } = useTranslation();
  const { token, applyNewToken } = useAuth();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
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
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div style={{ minHeight: "100vh", display: "flex", alignItems: "center", justifyContent: "center" }}>
      <form onSubmit={handleSubmit} className="panel" style={{ width: 360 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 16 }}>
          <div className="sidebar-brand-mark">A</div>
          <div>
            <div className="sidebar-brand-title">{t("changePassword.title")}</div>
            <div className="sidebar-brand-sub">ArgusOps</div>
          </div>
        </div>

        <p className="helper-text" style={{ marginBottom: 14 }}>
          {t("changePassword.hint")}
        </p>

        {error && <div className="error-banner">{error}</div>}

        <div className="field">
          <label htmlFor="currentPassword">{t("changePassword.currentPassword")}</label>
          <input
            id="currentPassword"
            className="input"
            type="password"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
            autoFocus
            required
          />
        </div>

        <div className="field">
          <label htmlFor="newPassword">{t("changePassword.newPassword")}</label>
          <input
            id="newPassword"
            className="input"
            type="password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            minLength={8}
            required
          />
        </div>

        <div className="field">
          <label htmlFor="confirmPassword">{t("changePassword.confirmPassword")}</label>
          <input
            id="confirmPassword"
            className="input"
            type="password"
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            minLength={8}
            required
          />
        </div>

        <button type="submit" className="btn btn-primary" style={{ width: "100%", justifyContent: "center" }} disabled={submitting}>
          {submitting ? t("changePassword.submitting") : t("changePassword.submit")}
        </button>
      </form>
    </div>
  );
}
