import { useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { mutationErrorMessage, useObject } from "../../api/hooks";
import { NIST_PHASE_ORDER } from "../../types/incidents";
import type { Playbook } from "../../types/playbooks";

// Read-only popup shown from an alert's "Related Playbook" panel -- same
// modal-overlay/modal pattern as CloseAlertModal and WebhooksPanel's
// EditWebhookModal. Editing still happens on the full PlaybookDetailPage
// (linked via "Edit playbook" at the bottom); this is for a responder
// glancing at the procedure and, for a containment step with a webhook
// configured, actually running the automation against the current alert.
export function PlaybookViewModal({ playbookId, alertId, onClose }: { playbookId: string; alertId: string; onClose: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data: playbook, loading, error: loadError } = useObject<Playbook>(
    (tok) => api.get<Playbook>(`/api/v1/playbooks/${playbookId}`, tok),
    [playbookId],
  );
  const [triggeringStepId, setTriggeringStepId] = useState<string | null>(null);
  const [triggerResults, setTriggerResults] = useState<Record<string, { success: boolean; message: string }>>({});

  async function trigger(stepId: string) {
    setTriggeringStepId(stepId);
    setTriggerResults((r) => {
      const next = { ...r };
      delete next[stepId];
      return next;
    });
    try {
      await api.post(`/api/v1/playbooks/steps/${stepId}/trigger`, { alertId }, token);
      setTriggerResults((r) => ({ ...r, [stepId]: { success: true, message: t("playbooks.view.triggerSuccess") } }));
    } catch (err) {
      setTriggerResults((r) => ({
        ...r,
        [stepId]: { success: false, message: t("playbooks.view.triggerError", { error: mutationErrorMessage(err) }) },
      }));
    } finally {
      setTriggeringStepId(null);
    }
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="panel-header">
          <h2 className="modal-title" style={{ marginBottom: 0 }}>
            {playbook?.title ?? "…"}
          </h2>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label={t("playbooks.view.close")}>
            ×
          </button>
        </div>

        {loading && <div className="empty-state">{t("common.loading")}</div>}
        {loadError && <div className="error-banner">{loadError}</div>}

        {playbook && (
          <>
            <p className="row-sub" style={{ marginTop: 0 }}>
              {playbook.category}
            </p>
            {playbook.description && (
              <p style={{ fontSize: 13, whiteSpace: "pre-wrap", marginTop: 8 }}>{playbook.description}</p>
            )}

            {NIST_PHASE_ORDER.map((phase) => {
              const steps = playbook.steps[phase];
              if (!steps || steps.length === 0) return null;
              return (
                <div key={phase} style={{ marginTop: 14 }}>
                  <p
                    style={{
                      fontSize: 12,
                      fontWeight: 700,
                      textTransform: "uppercase",
                      letterSpacing: "0.03em",
                      color: "var(--text-muted)",
                      margin: "0 0 6px",
                    }}
                  >
                    {t(`common.phase.${phase}`)}
                  </p>
                  <ol style={{ margin: 0, paddingLeft: 20, fontSize: 13, display: "flex", flexDirection: "column", gap: 8 }}>
                    {steps.map((step) => (
                      <li key={step.id}>
                        {step.text}
                        {step.webhookUrl && (
                          <div style={{ marginTop: 4 }}>
                            <button
                              type="button"
                              className="btn btn-sm"
                              onClick={() => trigger(step.id)}
                              disabled={triggeringStepId === step.id}
                            >
                              {triggeringStepId === step.id ? t("playbooks.view.triggering") : t("playbooks.view.triggerWebhook")}
                            </button>
                            {triggerResults[step.id] && (
                              <p
                                style={{
                                  fontSize: 11.5,
                                  marginTop: 4,
                                  color: triggerResults[step.id].success ? "var(--success)" : "var(--critical)",
                                }}
                              >
                                {triggerResults[step.id].message}
                              </p>
                            )}
                          </div>
                        )}
                      </li>
                    ))}
                  </ol>
                </div>
              );
            })}

            <div className="row-actions" style={{ marginTop: 16, justifyContent: "space-between" }}>
              <Link to={`/playbooks/${playbook.id}`} className="btn btn-ghost btn-sm">
                {t("playbooks.view.editPlaybook")}
              </Link>
              <button type="button" className="btn btn-sm" onClick={onClose}>
                {t("playbooks.view.close")}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
