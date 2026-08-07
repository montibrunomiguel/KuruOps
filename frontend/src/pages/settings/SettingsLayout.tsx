import { NavLink, Route, Routes } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { WebhooksPanel } from "./WebhooksPanel";
import { LLMProvidersPanel } from "./LLMProvidersPanel";
import { MCPServersPanel } from "./MCPServersPanel";
import { UsersPanel } from "./UsersPanel";
import { IdentityProvidersPanel } from "./IdentityProvidersPanel";
import { TagsPanel } from "./TagsPanel";
import { StorageIntegrationPanel } from "./StorageIntegrationPanel";
import { SMTPConfigPanel } from "./SMTPConfigPanel";
import { OnCallShiftsPanel } from "./OnCallShiftsPanel";
import { IncidentSLAPanel } from "./IncidentSLAPanel";
import { EscalationPoliciesPanel } from "./EscalationPoliciesPanel";
import { AuditExportPanel } from "./AuditExportPanel";
import { DatabaseMigrationPanel } from "./DatabaseMigrationPanel";

// Absolute paths -- a relative "to" (e.g. "webhooks") resolves against the
// *current* URL, not a fixed base, so clicking between tabs kept appending
// segments onto whatever tab you were already on (e.g.
// "/settings/webhooks/users/mcp-servers"). Every NavLink here needs the
// full /settings/... path so navigation is deterministic regardless of
// which tab you're currently viewing.
//
// Grouped into 4 categories instead of one flat 13-item list -- past ~7
// items in a single unlabeled group, scanning cost goes up (Krug's Trunk
// Test); grouping also gives each area a "why am I looking at this"
// signal the flat list didn't have.
const NAV_GROUPS = [
  {
    labelKey: "settings.navGroups.integrations",
    items: [
      { to: "/settings/webhooks", labelKey: "settings.nav.webhooks" },
      { to: "/settings/ai-integration", labelKey: "settings.nav.aiIntegration" },
      { to: "/settings/mcp-servers", labelKey: "settings.nav.mcpServers" },
      { to: "/settings/storage", labelKey: "settings.nav.storage" },
      { to: "/settings/smtp", labelKey: "settings.nav.smtp" },
    ],
  },
  {
    labelKey: "settings.navGroups.identityAccess",
    items: [
      { to: "/settings/users", labelKey: "settings.nav.users" },
      { to: "/settings/identity-providers", labelKey: "settings.nav.identityProviders" },
      { to: "/settings/tags", labelKey: "settings.nav.tags" },
    ],
  },
  {
    labelKey: "settings.navGroups.operations",
    items: [
      { to: "/settings/on-call-shifts", labelKey: "settings.nav.onCallShifts" },
      { to: "/settings/incident-sla", labelKey: "settings.nav.incidentSla" },
      { to: "/settings/escalation-policies", labelKey: "settings.nav.escalationPolicies" },
    ],
  },
  {
    labelKey: "settings.navGroups.dataAudit",
    items: [
      { to: "/settings/audit-export", labelKey: "settings.nav.auditExport" },
      { to: "/settings/database-migration", labelKey: "settings.nav.databaseMigration" },
    ],
  },
] as const;

export function SettingsLayout() {
  const { t } = useTranslation();
  return (
    <>
      <h1 className="page-title">{t("settings.title")}</h1>
      <p className="page-sub">{t("settings.subtitle")}</p>

      <div className="settings-layout">
        <nav className="settings-nav">
          {NAV_GROUPS.map((group) => (
            <div className="settings-nav-group" key={group.labelKey}>
              <div className="settings-nav-group-label">{t(group.labelKey)}</div>
              {group.items.map((item) => (
                <NavLink key={item.to} to={item.to} end>
                  {({ isActive }) => <span data-active={isActive}>{t(item.labelKey)}</span>}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>

        <div className="settings-panel">
          <Routes>
            <Route path="webhooks" element={<WebhooksPanel />} />
            <Route path="ai-integration" element={<LLMProvidersPanel />} />
            <Route path="mcp-servers" element={<MCPServersPanel />} />
            <Route path="tags" element={<TagsPanel />} />
            <Route path="users" element={<UsersPanel />} />
            <Route path="identity-providers" element={<IdentityProvidersPanel />} />
            <Route path="storage" element={<StorageIntegrationPanel />} />
            <Route path="smtp" element={<SMTPConfigPanel />} />
            <Route path="on-call-shifts" element={<OnCallShiftsPanel />} />
            <Route path="incident-sla" element={<IncidentSLAPanel />} />
            <Route path="escalation-policies" element={<EscalationPoliciesPanel />} />
            <Route path="audit-export" element={<AuditExportPanel />} />
            <Route path="database-migration" element={<DatabaseMigrationPanel />} />
          </Routes>
        </div>
      </div>
    </>
  );
}
