import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { mutationErrorMessage } from "../api/hooks";
import { BrandMark } from "../components/BrandMark";

export function LoginPage() {
  const { t } = useTranslation();
  const { loginLocal } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await loginLocal(email.trim(), password);
      // "/" resolves to /change-password or /settings/webhooks depending on
      // mustChangePassword -- see App.tsx's RequireAuth/catch-all routes.
      navigate("/", { replace: true });
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <form onSubmit={handleSubmit} className="panel" style={{ width: 340 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 20 }}>
          <BrandMark />
          <div>
            <div className="sidebar-brand-title">ArgusOps</div>
            <div className="sidebar-brand-sub">{t("sidebar.brandSub")}</div>
          </div>
        </div>

        {error && <div className="error-banner">{error}</div>}

        <div className="field">
          <label htmlFor="email">{t("login.email")}</label>
          <input
            id="email"
            className="input"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoFocus
            required
          />
        </div>

        <div className="field">
          <label htmlFor="password">{t("login.password")}</label>
          <input
            id="password"
            className="input"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </div>

        <button type="submit" className="btn btn-primary" style={{ width: "100%", justifyContent: "center" }} disabled={submitting}>
          {submitting ? t("login.submitting") : t("login.submit")}
        </button>

        <p className="helper-text" style={{ marginTop: 14, textAlign: "center" }}>
          <Link to="/forgot-password">{t("login.forgotPassword")}</Link>
        </p>

        <p className="helper-text" style={{ marginTop: 8, textAlign: "center" }}>
          {t("login.ssoHint")}
        </p>
      </form>
    </div>
  );
}
