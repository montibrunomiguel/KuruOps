import { useEffect, useRef, useState } from "react";
import { Navigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";

// SamlCallback is where the backend's ACS endpoint sends the browser once an
// IdP assertion has been accepted (see SAMLAuthService.ServeACS).
//
// Routed at /login/saml rather than anywhere under /auth/, because nginx
// proxies that whole prefix to the API -- a callback there never reaches
// this SPA at all.
//
// Nothing is passed in the URL. The ACS response set the HttpOnly refresh
// cookie and redirected here; this page trades that cookie for a session
// through the ordinary /auth/refresh call. A session token never reaches
// browser history, a proxy log, or any JavaScript on the page -- which is
// the whole reason this route exists rather than the ACS simply returning
// the token, as it used to.
export function SamlCallbackPage() {
  const { t } = useTranslation();
  const { adoptCookieSession, isAuthenticated } = useAuth();
  const [failed, setFailed] = useState(false);
  // React 18 runs effects twice in StrictMode, and this one consumes a
  // rotating credential -- the second call would present a token the first
  // has already spent.
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void adoptCookieSession().then((ok) => {
      if (!ok) setFailed(true);
    });
  }, [adoptCookieSession]);

  if (isAuthenticated) return <Navigate to="/dashboard" replace />;

  // A direct visit with no cookie lands here too, which is why this reads as
  // "sign in again" rather than an error: there is nothing for the person to
  // fix, and no detail worth showing them about someone else's IdP.
  if (failed) {
    return (
      <div className="auth-shell">
        <div className="auth-card">
          <div className="error-banner">{t("samlCallback.failed")}</div>
          <a className="btn btn-primary" href="/login">
            {t("samlCallback.backToLogin")}
          </a>
        </div>
      </div>
    );
  }

  return (
    <div className="auth-shell">
      <div className="auth-card">
        <p className="helper-text">{t("samlCallback.completing")}</p>
      </div>
    </div>
  );
}

export default SamlCallbackPage;
