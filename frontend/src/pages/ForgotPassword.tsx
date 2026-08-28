import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";
import { BrandMark } from "../components/BrandMark";

// Public, unauthenticated page -- POST /auth/password-reset/request always
// returns 204 regardless of whether the email matched a real account (see
// backend PasswordResetService.RequestReset), so this always shows the same
// generic "check your email" message, never a per-outcome one. That's not
// this page hiding an error; the backend genuinely gives it nothing else to
// show.
export function ForgotPasswordPage() {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await api.post("/auth/password-reset/request", { email: email.trim() }, null);
      setSubmitted(true);
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
            <div className="sidebar-brand-title">{t("forgotPassword.title")}</div>
            <div className="sidebar-brand-sub">KuruOps</div>
          </div>
        </div>

        {submitted ? (
          <p className="helper-text">{t("forgotPassword.submitted")}</p>
        ) : (
          <form onSubmit={handleSubmit}>
            <p className="helper-text" style={{ marginBottom: 14 }}>
              {t("forgotPassword.hint")}
            </p>
            {error && <div className="error-banner">{error}</div>}
            <div className="field">
              <label htmlFor="forgot-email">{t("login.email")}</label>
              <input
                id="forgot-email"
                className="input"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoFocus
                required
              />
            </div>
            <button type="submit" className="btn btn-primary" style={{ width: "100%", justifyContent: "center" }} disabled={submitting}>
              {submitting ? t("forgotPassword.submitting") : t("forgotPassword.submit")}
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
