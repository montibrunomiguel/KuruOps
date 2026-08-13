import { useState } from "react";
import { useTranslation } from "react-i18next";

export function PayloadPanel({ payload }: { payload: unknown }) {
  const { t } = useTranslation();
  // Collapsed by default -- the raw webhook payload is the least-read panel
  // during triage, but as the first panel on the page it was pushing the
  // actually-actionable ones (classification, linked alerts) below the fold.
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("alerts.detail.payloadTitle")}</h2>
        <button className="btn btn-ghost btn-sm" style={{ color: "var(--accent)" }} onClick={() => setExpanded((v) => !v)}>
          {expanded ? t("alerts.detail.collapse") : t("alerts.detail.expand")}
        </button>
      </div>
      {expanded && (
        <pre
          style={{
            margin: 0,
            fontSize: 11.5,
            fontFamily: "IBM Plex Mono, monospace",
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
            color: "var(--text-secondary)",
          }}
        >
          {JSON.stringify(payload, null, 2)}
        </pre>
      )}
    </div>
  );
}
