import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../api/client";
import { useList } from "../../api/hooks";
import type { FieldMappingTemplate, WebhookEndpoint } from "../../types/api";
import { expiryBadge } from "./WebhooksPanel/expiryBadge";
import { CreateWebhookModal } from "./WebhooksPanel/CreateWebhookModal";
import { EditWebhookModal } from "./WebhooksPanel/EditWebhookModal";

export function WebhooksPanel() {
  const { t } = useTranslation();
  const { data: endpoints, loading, error, reload } = useList<WebhookEndpoint>(
    (tk) => api.get<WebhookEndpoint[]>("/api/v1/settings/webhooks", tk),
  );
  // Fetched once here and passed down to both modals, instead of each of
  // them fetching its own copy.
  const { data: templates } = useList<FieldMappingTemplate>((tk) =>
    api.get<FieldMappingTemplate[]>("/api/v1/settings/field-mapping-templates", tk),
  );

  const [showCreate, setShowCreate] = useState(false);
  // Looked up by id against the live `endpoints` list (not a held snapshot)
  // so a save inside EditWebhookModal -- which reloads the list -- is
  // reflected immediately in the still-open modal, same "editingId" pattern
  // FieldMappingTemplatesPanel already uses for its own edit-in-place flow.
  const [editingEndpointId, setEditingEndpointId] = useState<string | null>(null);
  const editingEndpoint = endpoints?.find((ep) => ep.id === editingEndpointId) ?? null;
  const [newToken, setNewToken] = useState<{ name: string; token: string } | null>(null);

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.webhooks.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("settings.webhooks.newEndpoint")}
        </button>
      </div>

      {error && <div className="error-banner">{error}</div>}

      {newToken && (
        <div className="panel" style={{ background: "var(--accent-soft)", borderColor: "var(--accent)", marginBottom: 14 }}>
          <p style={{ margin: "0 0 8px", fontSize: 12.5 }}>
            {t("settings.webhooks.tokenGenerated", { name: newToken.name })}
          </p>
          <div className="token-reveal">
            <code style={{ fontSize: 12 }}>{newToken.token}</code>
            <button
              className="btn btn-sm"
              onClick={() => {
                navigator.clipboard.writeText(newToken.token);
              }}
            >
              {t("common.copy")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setNewToken(null)}>
              {t("common.close")}
            </button>
          </div>
        </div>
      )}

      {showCreate && (
        <CreateWebhookModal
          templates={templates ?? []}
          onCancel={() => setShowCreate(false)}
          onCreated={(name, tok) => {
            setShowCreate(false);
            setNewToken({ name, token: tok });
            reload();
          }}
        />
      )}

      {editingEndpoint && (
        <EditWebhookModal
          endpoint={editingEndpoint}
          templates={templates ?? []}
          onClose={() => setEditingEndpointId(null)}
          onChanged={reload}
          onRegenerated={(tok) => {
            setNewToken({ name: editingEndpoint.name, token: tok });
            setEditingEndpointId(null);
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && endpoints && endpoints.length === 0 && (
        <div className="empty-state">{t("settings.webhooks.noEndpoints")}</div>
      )}

      {!loading &&
        endpoints &&
        endpoints.map((ep) => (
          <div
            className="row"
            key={ep.id}
            style={{ cursor: "pointer" }}
            onClick={() => setEditingEndpointId(ep.id)}
          >
            <div className="row-main">
              <p className="row-title">
                {ep.name}{" "}
                <span className={`badge ${ep.status === "active" ? "badge-success" : "badge-muted"}`}>
                  <span className="badge-status-dot" />
                  {ep.status}
                </span>{" "}
                {expiryBadge(t, ep.expiresAt)}
              </p>
              <p className="row-sub">
                {ep.source} · token whk_••••••••{ep.tokenLast4}
              </p>
              <p className="row-sub">
                {ep.groupByFields.length
                  ? t("settings.webhooks.groupByFields.summary", {
                      fields: ep.groupByFields.join(", "),
                      minutes: ep.dedupWindowMinutes,
                    })
                  : t("settings.webhooks.groupByFields.summaryOff")}
              </p>
            </div>
          </div>
        ))}
    </div>
  );
}
