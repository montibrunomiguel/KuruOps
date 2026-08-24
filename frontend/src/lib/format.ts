import type { TFunction } from "i18next";
import i18n from "../i18n";

// currentLocale maps our two supported i18next language codes to a full BCP
// 47 locale for Intl formatters -- i18next itself is happy with bare "en"/
// "pt", but Intl.DateTimeFormat/RelativeTimeFormat want a region for
// sensible defaults (date order, AM/PM, etc).
export function currentLocale(): string {
  return i18n.language === "en" ? "en-US" : "pt-BR";
}

export function formatDateTime(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return d.toLocaleString(currentLocale(), { day: "2-digit", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

const RELATIVE_UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 60 * 60 * 24 * 365],
  ["month", 60 * 60 * 24 * 30],
  ["day", 60 * 60 * 24],
  ["hour", 60 * 60],
  ["minute", 60],
];

// Intl.RelativeTimeFormat handles the "2 hours ago" / "há 2 horas" phrasing
// natively per-locale -- no need to hand-maintain translated relative-time
// strings alongside the rest of i18n/locales/*.json.
export function formatRelative(iso?: string): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  const diffSec = Math.round((Date.now() - then) / 1000);
  const rtf = new Intl.RelativeTimeFormat(currentLocale(), { numeric: "auto" });

  if (diffSec < 60) return rtf.format(0, "second");
  for (const [unit, secondsInUnit] of RELATIVE_UNITS) {
    if (diffSec >= secondsInUnit) {
      return rtf.format(-Math.round(diffSec / secondsInUnit), unit);
    }
  }
  return rtf.format(-Math.round(diffSec / 60), "minute");
}

// s/m/h/d suffixes read the same in English and Portuguese, so no
// per-language unit map is needed here (unlike formatRelative's full
// "ago" phrasing, which does differ).
export function formatDuration(seconds?: number | null): string {
  if (seconds == null || Number.isNaN(seconds)) return "—";
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}

export function shortId(id: string): string {
  return id.slice(0, 8);
}

// initials renders a comment author's avatar letters -- first + last name
// initial, or "?" when there's no name at all.
export function initials(name?: string): string {
  if (!name) return "?";
  const parts = name.trim().split(/\s+/);
  const first = parts[0]?.[0] ?? "";
  const last = parts.length > 1 ? parts[parts.length - 1]?.[0] ?? "" : "";
  return (first + last).toUpperCase();
}

// validateNewPassword is the "set a new password" check shared by
// ChangePassword, ResetPassword, and Profile's password section --
// duplicated three times before this. Returns a translated error message,
// or null when the pair is valid.
export function validateNewPassword(newPassword: string, confirmPassword: string, t: TFunction): string | null {
  if (newPassword !== confirmPassword) return t("changePassword.mismatch");
  if (newPassword.length < 8) return t("changePassword.tooShort");
  // Mirrors the backend's validatePasswordPolicy (internal/service/password_policy.go)
  // so an all-letters or all-digits password is caught here instead of round-tripping
  // to the server first.
  if (!/[\p{L}]/u.test(newPassword) || !/[0-9]/.test(newPassword)) return t("changePassword.needsLetterAndDigit");
  return null;
}

// expiryOptions/expiryToDays back both the webhook-endpoint token expiry
// dropdown and the personal API token expiry dropdown -- same options
// (settings.webhooks.expiry.* copy is reused for both rather than adding a
// near-identical second i18n block).
export function expiryOptions(t: TFunction): { value: string; label: string }[] {
  return [
    { value: "30", label: t("settings.webhooks.expiry.30") },
    { value: "90", label: t("settings.webhooks.expiry.90") },
    { value: "180", label: t("settings.webhooks.expiry.180") },
    { value: "365", label: t("settings.webhooks.expiry.365") },
    { value: "never", label: t("settings.webhooks.expiry.never") },
  ];
}

export function expiryToDays(value: string): number | undefined {
  // undefined -> let the backend apply its own default (90d); "never" -> 0,
  // which service.resolveExpiry treats as an explicit opt-out.
  if (value === "never") return 0;
  return Number(value);
}
