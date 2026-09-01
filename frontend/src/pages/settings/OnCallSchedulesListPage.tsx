import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { OnCallSchedule } from "../../types/onCallSchedule";

// Settings -> Escalas de Atendimento: any number of named on-call
// rotations per tenant (one per team/product, say), one of them marked
// default -- that's the one AlertService.Ingest and cmd/worker's
// escalation sweep actually resolve against for automatic assignment. See
// OnCallScheduleDetailPage for the per-schedule editor this list links
// into.
export function OnCallSchedulesListPage() {
  const { t } = useTranslation();
  const { token } = useAuth();
  const navigate = useNavigate();
  const { data: schedules, loading, error, reload } = useList<OnCallSchedule>(["on-call-schedules"], (tk) =>
    api.get<OnCallSchedule[]>("/api/v1/settings/on-call-schedules", tk),
  );
  const [settingDefaultId, setSettingDefaultId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  async function setDefault(id: string) {
    setSettingDefaultId(id);
    setActionError(null);
    try {
      await api.post(`/api/v1/settings/on-call-schedules/${id}/default`, {}, token);
      reload();
    } catch (err) {
      setActionError(mutationErrorMessage(err));
    } finally {
      setSettingDefaultId(null);
    }
  }

  if (loading) {
    return (
      <div className="panel">
        <div className="empty-state">{t("common.loading")}</div>
      </div>
    );
  }

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.onCallSchedule.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => navigate("/settings/on-call-schedules/new")}>
          {t("settings.onCallSchedule.newSchedule")}
        </button>
      </div>
      <p className="helper-text" style={{ marginTop: -8, marginBottom: 14 }}>
        {t("settings.onCallSchedule.helper")}
      </p>

      {error && <div className="error-banner">{error}</div>}
      {actionError && <div className="error-banner">{actionError}</div>}

      {schedules && schedules.length === 0 && <div className="empty-state">{t("settings.onCallSchedule.noSchedules")}</div>}

      {schedules?.map((s) => (
        <div
          key={s.id}
          className="row"
          style={{ cursor: "pointer" }}
          onClick={() => navigate(`/settings/on-call-schedules/${s.id}`)}
        >
          <div className="row-main">
            <p className="row-title">
              {s.name}
              {s.isDefault && (
                <span className="badge badge-muted" style={{ marginLeft: 8 }}>
                  {t("settings.onCallSchedule.default")}
                </span>
              )}
              {/* A schedule with nobody on it is a valid configuration --
                  webhook, PagerDuty and Slack steps all fire to a destination
                  regardless of who is on call -- but it puts nobody on call
                  and leaves the analyst name/email/phone blank in every
                  notification it feeds. Worth saying out loud in the list
                  rather than leaving it to be inferred from "0 responders". */}
              {s.participants.length === 0 && (
                <span className="badge badge-critical" style={{ marginLeft: 8 }}>
                  {t("settings.onCallSchedule.noParticipantsBadge")}
                </span>
              )}
            </p>
            <p className="row-sub">{t("settings.onCallSchedule.respondersCount", { count: s.participants.length })}</p>
          </div>
          {!s.isDefault && (
            <div className="row-actions">
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                disabled={settingDefaultId === s.id}
                onClick={(e) => {
                  e.stopPropagation();
                  setDefault(s.id);
                }}
              >
                {settingDefaultId === s.id ? t("common.saving") : t("settings.onCallSchedule.setDefault")}
              </button>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
