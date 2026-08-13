import { useTranslation } from "react-i18next";
import type { Alert } from "../../../types/alerts";
import { ClassificationBadge } from "../../../components/badges";
import { AttachmentPreview } from "../../../components/AttachmentButton";

// ClassificationPanel is purely informational now -- the trigger to close
// and classify an alert lives in the header toolbar (see the "Fechar e
// Classificar" button in AlertDetailPage, next to Analisar com IA) and
// opens CloseAlertModal, rather than an inline form here. Renders nothing
// until the alert is actually closed and classified, so .detail-main
// doesn't show an empty panel shell in the meantime.
export function ClassificationPanel({ alert }: { alert: Alert }) {
  const { t } = useTranslation();
  if (!(alert.status === "closed" && alert.classification)) return null;

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.classificationTitle")}
      </h2>
      <ClassificationBadge classification={alert.classification} />
      {alert.closeComment && (
        <p style={{ margin: "10px 0 0", fontSize: 13, color: "var(--text-secondary)" }}>{alert.closeComment}</p>
      )}
      {alert.closeAttachmentUrl && <AttachmentPreview url={alert.closeAttachmentUrl} />}
    </div>
  );
}
