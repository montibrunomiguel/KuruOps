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
  const [exportingFormat, setExportingFormat] = useState<"cef" | "json" | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function exportFormat(format: "cef" | "json") {
    setExportingFormat(format);
    setError(null);
    try {
      const { blob, filename } = await api.downloadFile(`/api/v1/settings/audit-export/${format}`, token);
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
      setExportingFormat(null);
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

      <div style={{ display: "flex", gap: 8 }}>
        <button className="btn btn-primary" onClick={() => exportFormat("cef")} disabled={exportingFormat !== null}>
          {exportingFormat === "cef" ? t("settings.auditExport.exporting") : t("settings.auditExport.export")}
        </button>
        <button className="btn btn-secondary" onClick={() => exportFormat("json")} disabled={exportingFormat !== null}>
          {exportingFormat === "json" ? t("settings.auditExport.exporting") : t("settings.auditExport.exportJson")}
        </button>
      </div>
    </div>
  );
}
