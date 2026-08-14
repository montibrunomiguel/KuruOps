import type { TFunction } from "i18next";
import type { ActivityEvent } from "../types/dashboard";
import { BellIcon, CheckIcon, ClockIcon, CommentIcon, FlagIcon, LinkIcon, ShieldIcon, SparkleIcon } from "../components/icons";
import { shortId } from "./format";

interface Described {
  icon: React.ReactNode;
  tone: string;
  text: string;
}

// Turns one alert_events/incident_events row (see domain.ActivityEvent) into
// an icon + tone + translated sentence for the Dashboard's Recent Activity
// feed. `data` is whatever json each event type's writer put there --
// AlertService/IncidentService's various json.Marshal(map[string]...)
// calls -- so every field access here is defensive (never assume a key
// exists).
export function describeActivity(e: ActivityEvent, t: TFunction): Described {
  const data = (e.data ?? {}) as Record<string, unknown>;
  const id = shortId(e.contextId);
  const str = (v: unknown, fallback = "") => (typeof v === "string" ? v : fallback);

  if (e.kind === "alert") {
    switch (e.eventType) {
      case "received":
        return { icon: <BellIcon />, tone: "tone-accent", text: t("dashboard.activity.alert.received", { source: e.contextTitle }) };
      case "status_changed":
        return { icon: <ClockIcon />, tone: "tone-accent", text: t("dashboard.activity.alert.status_changed", { id }) };
      case "severity_overridden":
        return { icon: <FlagIcon />, tone: "tone-high", text: t("dashboard.activity.alert.severity_overridden", { id }) };
      case "closed": {
        const classification = str(data.classification, "");
        return {
          icon: <CheckIcon />,
          tone: "tone-success",
          text: t("dashboard.activity.alert.closed", {
            id,
            classification: classification ? t(`common.classification.${classification}`) : "—",
          }),
        };
      }
      case "escalated":
        return {
          icon: <FlagIcon />,
          tone: "tone-critical",
          text: t("dashboard.activity.alert.escalated", { id, incidentId: shortId(str(data.incidentId, "")) }),
        };
      case "tags_changed":
        return { icon: <ClockIcon />, tone: "tone-muted", text: t("dashboard.activity.alert.tags_changed", { id }) };
      case "linked":
        return {
          icon: <LinkIcon />,
          tone: "tone-accent",
          text: t("dashboard.activity.alert.linked", { id, linkedId: shortId(str(data.alertId, "")) }),
        };
      case "ai_analysis_run":
        return { icon: <SparkleIcon />, tone: "tone-accent", text: t("dashboard.activity.alert.ai_analysis_run", { id }) };
      case "assignee_changed":
        return { icon: <ClockIcon />, tone: "tone-accent", text: t("dashboard.activity.alert.assignee_changed", { id }) };
      case "duplicate_suppressed": {
        const duplicateCount = typeof data.duplicateCount === "number" ? data.duplicateCount : 0;
        return {
          icon: <ClockIcon />,
          tone: "tone-muted",
          text: t("dashboard.activity.alert.duplicate_suppressed", { id, count: duplicateCount }),
        };
      }
      case "playbook_webhook_triggered": {
        const success = data.success === true;
        return {
          icon: <FlagIcon />,
          tone: success ? "tone-accent" : "tone-critical",
          text: t(`dashboard.activity.alert.playbook_webhook_triggered_${success ? "success" : "failure"}`, { id }),
        };
      }
      default:
        return { icon: <ClockIcon />, tone: "tone-muted", text: `${e.eventType.replace(/_/g, " ")} (${id})` };
    }
  }

  switch (e.eventType) {
    case "created":
      return { icon: <FlagIcon />, tone: "tone-accent", text: t("dashboard.activity.incident.created", { id }) };
    case "phase_changed":
      return { icon: <ShieldIcon />, tone: "tone-accent", text: t("dashboard.activity.incident.phase_changed", { id }) };
    case "phase_skipped":
      return { icon: <ShieldIcon />, tone: "tone-high", text: t("dashboard.activity.incident.phase_skipped", { id }) };
    case "closed":
      return { icon: <CheckIcon />, tone: "tone-success", text: t("dashboard.activity.incident.closed", { id }) };
    case "severity_priority_changed":
      return { icon: <FlagIcon />, tone: "tone-high", text: t("dashboard.activity.incident.severity_priority_changed", { id }) };
    case "description_edited":
      return { icon: <ClockIcon />, tone: "tone-muted", text: t("dashboard.activity.incident.description_edited", { id }) };
    case "tags_changed":
      return { icon: <ClockIcon />, tone: "tone-muted", text: t("dashboard.activity.incident.tags_changed", { id }) };
    case "alert_linked":
      return {
        icon: <LinkIcon />,
        tone: "tone-accent",
        text: t("dashboard.activity.incident.alert_linked", { id, alertId: shortId(str(data.alertId, "")) }),
      };
    case "alert_unlinked":
      return {
        icon: <LinkIcon />,
        tone: "tone-muted",
        text: t("dashboard.activity.incident.alert_unlinked", { id, alertId: shortId(str(data.alertId, "")) }),
      };
    case "ai_analysis_run":
      return { icon: <SparkleIcon />, tone: "tone-accent", text: t("dashboard.activity.incident.ai_analysis_run", { id }) };
    case "status_timestamp_corrected":
      return { icon: <ClockIcon />, tone: "tone-muted", text: t("dashboard.activity.incident.status_timestamp_corrected", { id }) };
    case "comment_added":
      return {
        icon: <CommentIcon />,
        tone: "tone-accent",
        text: t("dashboard.activity.incident.comment_added", { id, actor: str(data.authorName, "—") }),
      };
    case "assignees_changed":
      return { icon: <ShieldIcon />, tone: "tone-accent", text: t("dashboard.activity.incident.assignees_changed", { id }) };
    case "role_assigned":
    case "role_unassigned":
      return { icon: <ShieldIcon />, tone: "tone-accent", text: t("dashboard.activity.incident.role_changed", { id }) };
    default:
      return { icon: <ClockIcon />, tone: "tone-muted", text: `${e.eventType.replace(/_/g, " ")} (${id})` };
  }
}
