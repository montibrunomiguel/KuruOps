import { useTranslation } from "react-i18next";

// TempPasswordBanner is the "show a one-time password exactly once" UX
// shared by user creation and password reset -- same admin-shares-it-out-of-
// band, forced-change-on-next-login flow either way, so the display is
// identical, only the title/helper text differ.
export function TempPasswordBanner({
  title,
  helper,
  temporaryPassword,
  onClose,
}: {
  title: string;
  helper: string;
  temporaryPassword: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="panel" style={{ marginBottom: 14, borderColor: "var(--accent)" }}>
      <p className="row-title" style={{ marginBottom: 4 }}>
        {title}
      </p>
      <p className="helper-text" style={{ marginBottom: 10 }}>
        {helper}
      </p>
      <code className="mono" style={{ display: "block", padding: 8, background: "var(--surface-2)", borderRadius: 6, wordBreak: "break-all" }}>
        {temporaryPassword}
      </code>
      <div className="row-actions" style={{ marginTop: 10 }}>
        <button className="btn btn-sm" onClick={onClose}>
          {t("common.close")}
        </button>
      </div>
    </div>
  );
}
