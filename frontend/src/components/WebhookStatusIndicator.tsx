import { useTranslation } from "react-i18next";

// A static "online" indicator -- cmd/ingest is a stateless HTTP service with
// no push-based health channel to the frontend yet, so this reflects that
// the ingest path exists and is reachable by design, not a live heartbeat.
// Shown in the top-right corner of every main page, matching the design.
export function WebhookStatusIndicator() {
  const { t } = useTranslation();
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12, color: "var(--text-secondary)" }}>
      <span style={{ width: 7, height: 7, borderRadius: "50%", background: "var(--success)", display: "inline-block" }} />
      {t("common.webhookOnline")}
    </div>
  );
}
