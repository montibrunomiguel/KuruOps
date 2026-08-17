import { formatDateTime } from "../../../lib/format";

// EXPIRING_SOON_DAYS controls when a live token starts showing the
// "expiring soon" warning instead of the plain expiry date -- gives an
// admin a heads-up window to regenerate before ingest starts rejecting it.
const EXPIRING_SOON_DAYS = 14;

export function expiryBadge(t: (k: string, opts?: Record<string, unknown>) => string, expiresAt?: string) {
  if (!expiresAt) return <span className="badge badge-muted">{t("settings.webhooks.badge.neverExpires")}</span>;
  const diffDays = (new Date(expiresAt).getTime() - Date.now()) / 86_400_000;
  if (diffDays < 0) {
    return (
      <span className="badge badge-critical">
        {t("settings.webhooks.badge.expiredOn", { date: formatDateTime(expiresAt) })}
      </span>
    );
  }
  if (diffDays <= EXPIRING_SOON_DAYS) {
    return (
      <span className="badge badge-sev-high">
        {t("settings.webhooks.badge.expiringSoon", { date: formatDateTime(expiresAt) })}
      </span>
    );
  }
  return (
    <span className="badge badge-muted">
      {t("settings.webhooks.badge.expiresOn", { date: formatDateTime(expiresAt) })}
    </span>
  );
}
