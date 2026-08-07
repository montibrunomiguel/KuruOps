import { Navigate, NavLink, Route, Routes } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { WebhookStatusIndicator } from "../../components/WebhookStatusIndicator";
import { AlertsTabPanel } from "./AlertsTabPanel";
import { IncidentsTabPanel } from "./IncidentsTabPanel";
import { FollowupTabPanel } from "./FollowupTabPanel";

// `segment` is what the nested <Route path> matches against (relative,
// matched within this component's own <Routes>); `to` is the absolute path
// used for navigation. A relative NavLink/Navigate "to" resolves against
// the *current* URL, not a fixed base -- inside a component already
// mounted at /dashboard/*, that turned "alerts" into "/dashboard/alerts/alerts"
// once you'd already navigated to a tab (same bug fixed in SettingsLayout).
const TAB_DEFS = [
  { segment: "alerts", to: "/dashboard/alerts", labelKey: "dashboard.tabs.alerts", capability: "alerts" },
  { segment: "incidents", to: "/dashboard/incidents", labelKey: "dashboard.tabs.incidents", capability: "incidents" },
  { segment: "followup", to: "/dashboard/followup", labelKey: "dashboard.tabs.followup", capability: "followup" },
];

// Each Dashboard tab is its own route, gated independently by the
// corresponding resourceAccess capability -- see AuthContext.hasResourceAccess
// and middleware.RequireResourceAccess on the backend. This is what lets a
// SOC analyst be granted Follow-up without Incidents, and a CSIRT member any
// combination up to all three, rather than the old single tabbed page where
// every visitor saw every tab regardless of access. Each tab panel owns its
// own stat cards, charts, and tables -- they differ enough per tab (see the
// design handoff screenshots) that a single shared stats header no longer
// fits all three.
export function DashboardLayout() {
  const { t } = useTranslation();
  const { hasResourceAccess } = useAuth();
  const tabs = TAB_DEFS.filter((t2) => hasResourceAccess(t2.capability));
  const firstTab = tabs[0]?.to;

  return (
    <div>
      <div className="toolbar">
        <div className="toolbar-title">
          <h1 className="page-title">{t("dashboard.title")}</h1>
          <p className="page-sub" style={{ marginBottom: 0 }}>
            {t("dashboard.subtitle")}
          </p>
        </div>
        <WebhookStatusIndicator />
      </div>

      {tabs.length === 0 ? (
        <div className="panel">
          <div className="empty-state">{t("dashboard.noAccess")}</div>
        </div>
      ) : (
        <>
          <div className="pill-tabs" style={{ marginBottom: 18 }}>
            {tabs.map((tab) => (
              <NavLink key={tab.segment} to={tab.to} style={{ textDecoration: "none" }}>
                {({ isActive }: { isActive: boolean }) => (
                  <span className="pill-tab" data-active={isActive}>
                    {t(tab.labelKey)}
                  </span>
                )}
              </NavLink>
            ))}
          </div>

          <Routes>
            <Route index element={<Navigate to={firstTab} replace />} />
            {hasResourceAccess("alerts") && <Route path="alerts" element={<AlertsTabPanel />} />}
            {hasResourceAccess("incidents") && <Route path="incidents" element={<IncidentsTabPanel />} />}
            {hasResourceAccess("followup") && <Route path="followup" element={<FollowupTabPanel />} />}
            <Route path="*" element={<Navigate to={firstTab} replace />} />
          </Routes>
        </>
      )}
    </div>
  );
}
