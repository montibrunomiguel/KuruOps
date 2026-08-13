import { useTranslation } from "react-i18next";

// MetadataPanel is the sender's own curated key/value list (Slack channel,
// playbook/runbook link, environment, anything else they chose to send --
// see backend domain.Alert.Metadata) -- unlike PayloadPanel, this is not
// collapsed by default: it's specifically the subset of information the
// source considered important enough to call out. Renders nothing at all
// when metadata is empty, rather than an empty panel shell.
export function MetadataPanel({ metadata }: { metadata: Record<string, unknown> | undefined }) {
  const { t } = useTranslation();
  const entries = Object.entries(metadata ?? {});
  if (entries.length === 0) return null;

  return (
    <div className="panel" style={{ marginBottom: 16 }}>
      <h2 className="panel-title" style={{ marginBottom: 10 }}>
        {t("alerts.detail.customMetadataTitle")}
      </h2>
      <div className="breakdown-list">
        {entries.map(([key, value]) => {
          const text = typeof value === "string" ? value : JSON.stringify(value);
          const isLink = typeof value === "string" && /^https?:\/\//i.test(value);
          return (
            <div key={key} className="legend-row">
              <span className="legend-row-label" style={{ fontWeight: 600 }}>
                {key}
              </span>
              <span className="legend-row-value" style={{ wordBreak: "break-word", textAlign: "right" }}>
                {isLink ? (
                  <a href={value as string} target="_blank" rel="noopener noreferrer">
                    {text}
                  </a>
                ) : (
                  text
                )}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
