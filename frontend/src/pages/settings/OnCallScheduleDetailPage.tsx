import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList } from "../../api/hooks";
import type { OnCallSchedule } from "../../types/onCallSchedule";
import type { UserSummary } from "../../types/users";
import { OnCallTimeline } from "../../components/OnCallTimeline";
import { ScheduleForm } from "./OnCallScheduleDetailPage/ScheduleForm";

// Settings -> On-Call Schedules -> one schedule's editor: ordered
// responders, a handover time + cadence the system uses to compute whose
// turn it is (no manual per-day blocks), optional concurrent shifts,
// optional working-hours restriction, and per-day overrides -- modeled on
// incident.io's "Create a new schedule" flow. Scoped to useParams<{id}>(),
// id === "new" is create mode (same pattern as PlaybookDetailPage). See
// backend/internal/domain.ResolveOnCallSet for the rotation math this form
// configures, and OnCallTimeline for the live preview below the form.
export function OnCallScheduleDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const isNew = id === "new";
  const { token } = useAuth();
  const navigate = useNavigate();

  const {
    data: loaded,
    loading,
    error,
    reload,
  } = useList<OnCallSchedule>(
    ["on-call-schedule-detail", id],
    async (tk) => {
      if (isNew || !id) return [];
      return [await api.get<OnCallSchedule>(`/api/v1/settings/on-call-schedules/${id}`, tk)];
    },
  );
  const schedule = loaded?.[0] ?? null;

  const { data: directory } = useList<UserSummary>(["users-directory"], (tk) => api.get<UserSummary[]>("/api/v1/users/directory", tk));

  if (!isNew && loading) {
    return (
      <div className="panel">
        <div className="empty-state">{t("common.loading")}</div>
      </div>
    );
  }
  if (!isNew && (error || !schedule)) {
    return (
      <div className="panel">
        <div className="empty-state">{error ?? t("settings.onCallSchedule.notFound")}</div>
      </div>
    );
  }

  return (
    <div>
      <Link to="/settings/on-call-schedules" className="back-link">
        {t("settings.onCallSchedule.backToList")}
      </Link>

      <div className="panel" style={{ marginTop: 8 }}>
        <div className="panel-header">
          <h2 className="panel-title">{isNew ? t("settings.onCallSchedule.newSchedule") : schedule!.name}</h2>
        </div>

        <ScheduleForm schedule={schedule} isNew={isNew} directory={directory ?? []} onSaved={reload} token={token} navigate={navigate} />
      </div>

      {!isNew && schedule && <OnCallTimeline schedule={schedule} directory={directory ?? []} onOverrideChange={reload} />}
    </div>
  );
}
