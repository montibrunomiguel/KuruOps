import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { mutationErrorMessage } from "../api/hooks";
import { BrandMark } from "../components/BrandMark";

export function LoginPage() {
  const { t } = useTranslation();
  const { loginLocal, verifyMfa } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Set once loginLocal reports the account has TOTP enrolled -- while this
  // is non-null the form below renders the code-entry step instead of the
  // email/password one. Cleared on "Back" (returns to the first step) or on
  // an unmount, never persisted -- a page refresh mid-MFA just starts the
  // login over, same as the pending token's own short server-side TTL
  // already implies.
  const [pendingToken, setPendingToken] = useState<string | null>(null);
  const [code, setCode] = useState("");

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const result = await loginLocal(email.trim(), password);
      if (result.mfaRequired) {
        setPendingToken(result.pendingToken);
        return;
      }
      // "/" resolves to /change-password or /settings/webhooks depending on
      // mustChangePassword -- see App.tsx's RequireAuth/catch-all routes.
      navigate("/", { replace: true });
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleVerifyMfa(e: FormEvent) {
    e.preventDefault();
    if (!pendingToken) return;
    setSubmitting(true);
    setError(null);
    try {
      await verifyMfa(pendingToken, code.trim());
      navigate("/", { replace: true });
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  function handleBack() {
    setPendingToken(null);
    setCode("");
    setError(null);
  }

  return (
    <div className="auth-shell">
      {pendingToken === null ? (
        <form onSubmit={handleSubmit} className="auth-card">
          <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 20 }}>
            <BrandMark />
            <div>
              <div className="sidebar-brand-title">KuruOps</div>
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
      ) : (
        <form onSubmit={handleVerifyMfa} className="auth-card">
          <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 20 }}>
            <BrandMark />
            <div>
              <div className="sidebar-brand-title">KuruOps</div>
              <div className="sidebar-brand-sub">{t("sidebar.brandSub")}</div>
            </div>
          </div>

          <h2 className="panel-title" style={{ marginBottom: 4 }}>
            {t("login.mfa.title")}
          </h2>
          <p className="helper-text" style={{ marginBottom: 16 }}>
            {t("login.mfa.helper")}
          </p>

          {error && <div className="error-banner">{error}</div>}

          <div className="field">
            <label htmlFor="mfa-code">{t("login.mfa.codeLabel")}</label>
            <input
              id="mfa-code"
              className="input"
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              autoFocus
              required
            />
          </div>

          <button type="submit" className="btn btn-primary" style={{ width: "100%", justifyContent: "center" }} disabled={submitting || code.length !== 6}>
            {submitting ? t("login.mfa.verifying") : t("login.mfa.verify")}
          </button>

          <button
            type="button"
            className="btn btn-ghost btn-sm"
            style={{ width: "100%", justifyContent: "center", marginTop: 10 }}
            onClick={handleBack}
            disabled={submitting}
          >
            {t("login.mfa.back")}
          </button>
        </form>
      )}
    </div>
  );
}
