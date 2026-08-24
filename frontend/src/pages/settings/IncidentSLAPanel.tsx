import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { mutationErrorMessage, useList } from "../../api/hooks";
import { useSavedFlag } from "../../hooks/useSavedFlag";
import type { IncidentSLAPolicy, IncidentPriority } from "../../types/incidents";
import type { Severity } from "../../types/alerts";
import { PRIORITY_ORDER } from "../../lib/chartColors";
import { SeverityPriorityGrid, SEVERITY_PRIORITY_GRID_SEVERITIES } from "../../components/SeverityPriorityGrid";

function cellKey(severity: Severity, priority: IncidentPriority) {
  return `${severity}-${priority}`;
}

// Settings -> Incident SLAs: a severity x priority grid, same shape as the
// incident detail page's NIST matrix but editable -- each cell is a "due
// within N minutes" value, blank meaning unconfigured (opt-in per pair, not
// zero minutes). Dirty-tracking is per cell, but there's a single "Save
// changes" button rather than UsersPanel.tsx's per-row button, since a row
// here spans 4 independently-configurable cells rather than one form.
export function IncidentSLAPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const { data, loading, error, reload } = useList<IncidentSLAPolicy>(["incident-sla"], (tok) =>
    api.get<IncidentSLAPolicy[]>("/api/v1/settings/incident-sla", tok),
  );
  const policies = data ?? [];
  const [values, setValues] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const { saved, markSaved, clearSaved } = useSavedFlag();

  useEffect(() => {
    const next: Record<string, string> = {};
    for (const p of policies) {
      next[cellKey(p.severity, p.priority)] = String(p.dueWithinMinutes);
    }
    setValues(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  function original(key: string): string {
    const p = policies.find((p) => cellKey(p.severity, p.priority) === key);
    return p ? String(p.dueWithinMinutes) : "";
  }

  function isDirty(key: string): boolean {
    return (values[key] ?? "") !== original(key);
  }

  const allKeys = SEVERITY_PRIORITY_GRID_SEVERITIES.flatMap((sev) => PRIORITY_ORDER.map((p) => cellKey(sev, p)));
  const dirtyKeys = allKeys.filter(isDirty);

  async function handleSave() {
    setSaving(true);
    setSaveError(null);
    clearSaved();
    try {
      for (const key of dirtyKeys) {
        const [severity, priority] = key.split("-") as [Severity, IncidentPriority];
        const value = values[key] ?? "";
        const existing = policies.find((p) => cellKey(p.severity, p.priority) === key);
        if (value === "") {
          if (existing) {
            await api.del(`/api/v1/settings/incident-sla/${existing.id}`, token);
          }
          continue;
        }
        await api.put("/api/v1/settings/incident-sla", { severity, priority, dueWithinMinutes: Number(value) }, token);
      }
      markSaved();
      reload();
    } catch (err) {
      setSaveError(mutationErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  if (loading) return <div className="panel"><div className="empty-state">{t("common.loading")}</div></div>;

  return (
    <div className="panel">
      <h2 className="panel-title" style={{ marginBottom: 4 }}>
        {t("settings.incidentSla.title")}
      </h2>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.incidentSla.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {saveError && <div className="error-banner">{saveError}</div>}
      {saved && <div className="helper-text" style={{ color: "var(--success)", marginBottom: 12 }}>{t("settings.incidentSla.saved")}</div>}

      <SeverityPriorityGrid
        renderCell={(sev, p) => {
          const key = cellKey(sev, p);
          return (
            <div style={{ position: "relative" }}>
              <input
                className="input"
                type="number"
                min={1}
                style={{ width: "100%", textAlign: "center", paddingRight: values[key] ? 28 : undefined }}
                placeholder={t("settings.incidentSla.unconfigured") ?? undefined}
                aria-label={`${t(`common.severity.${sev}`)} / ${p.toUpperCase()}`}
                value={values[key] ?? ""}
                onChange={(e) => setValues((prev) => ({ ...prev, [key]: e.target.value }))}
              />
              {values[key] && (
                <span
                  style={{
                    position: "absolute",
                    right: 8,
                    top: "50%",
                    transform: "translateY(-50%)",
                    fontSize: 10,
                    color: "var(--text-muted)",
                    pointerEvents: "none",
                  }}
                >
                  {t("settings.incidentSla.unit")}
                </span>
              )}
            </div>
          );
        }}
      />

      <div className="row-actions" style={{ marginTop: 14 }}>
        <button type="button" className="btn btn-primary btn-sm" onClick={handleSave} disabled={saving || dirtyKeys.length === 0}>
          {saving ? t("common.saving") : t("common.save")}
        </button>
      </div>
    </div>
  );
}
