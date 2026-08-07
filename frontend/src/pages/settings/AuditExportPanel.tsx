import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { mutationErrorMessage } from "../../api/hooks";

// Settings -> Audit Export: a pull/download of the tenant's full
// alert/incident event history as CEF (Common Event Format), for feeding
// into a SIEM. See AuditExportHandlers on the backend -- v1 is a manual
// button, not a persistent push-to-syslog connection.
export function AuditExportPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [exporting, setExporting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function exportCEF() {
    setExporting(true);
    setError(null);
    try {
      const { blob, filename } = await api.downloadFile("/api/v1/settings/audit-export/cef", token);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setExporting(false);
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 4 }}>
        {t("settings.auditExport.title")}
      </h2>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.auditExport.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}

      <button className="btn btn-primary" onClick={exportCEF} disabled={exporting}>
        {exporting ? t("settings.auditExport.exporting") : t("settings.auditExport.export")}
      </button>
    </div>
  );
}
