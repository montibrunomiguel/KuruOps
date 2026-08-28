import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";
import { BrandMark } from "../components/BrandMark";
import { validateNewPassword } from "../lib/format";

// Public, unauthenticated page reached from the link in the reset email
// (?token=...). POST /auth/password-reset/confirm is single-use -- see
// PasswordResetService.ConfirmReset -- so a second submit with the same
// token (double-click, back button) surfaces the backend's "invalid or
// expired reset link" error rather than silently succeeding twice.
export function ResetPasswordPage() {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const token = searchParams.get("token") ?? "";
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [done, setDone] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    const validationError = validateNewPassword(newPassword, confirmPassword, t);
    if (validationError) {
      setError(validationError);
      return;
    }

    setSubmitting(true);
    try {
      await api.post("/auth/password-reset/confirm", { token, newPassword }, null);
      setDone(true);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div style={{ minHeight: "100vh", display: "flex", alignItems: "center", justifyContent: "center" }}>
      <div className="panel" style={{ width: 340 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 20 }}>
          <BrandMark />
          <div>
            <div className="sidebar-brand-title">{t("resetPasswordPage.title")}</div>
            <div className="sidebar-brand-sub">KuruOps</div>
          </div>
        </div>

        {done ? (
          <p className="helper-text">{t("resetPasswordPage.done")}</p>
        ) : !token ? (
          <p className="error-banner">{t("resetPasswordPage.missingToken")}</p>
        ) : (
          <form onSubmit={handleSubmit}>
            {error && <div className="error-banner">{error}</div>}
            <div className="field">
              <label htmlFor="reset-new-password">{t("changePassword.newPassword")}</label>
              <input
                id="reset-new-password"
                className="input"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                minLength={8}
                autoFocus
                required
              />
            </div>
            <div className="field">
              <label htmlFor="reset-confirm-password">{t("changePassword.confirmPassword")}</label>
              <input
                id="reset-confirm-password"
                className="input"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                minLength={8}
                required
              />
            </div>
            <button type="submit" className="btn btn-primary" style={{ width: "100%", justifyContent: "center" }} disabled={submitting}>
              {submitting ? t("changePassword.submitting") : t("resetPasswordPage.submit")}
            </button>
          </form>
        )}

        <p className="helper-text" style={{ marginTop: 14, textAlign: "center" }}>
          <Link to="/login">{t("forgotPassword.backToLogin")}</Link>
        </p>
      </div>
    </div>
  );
}
