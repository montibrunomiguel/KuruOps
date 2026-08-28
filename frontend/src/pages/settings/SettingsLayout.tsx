import { useState } from "react";
import { NavLink, Route, Routes, useLocation } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { ErrorBoundary } from "../../components/ErrorBoundary";
import { ChevronIcon } from "../../components/icons";
import { WebhooksPanel } from "./WebhooksPanel";
import { FieldMappingTemplatesPanel } from "./FieldMappingTemplatesPanel";
import { LLMProvidersPanel } from "./LLMProvidersPanel";
import { MCPServersPanel } from "./MCPServersPanel";
import { UsersPanel } from "./UsersPanel";
import { RolesPanel } from "./RolesPanel";
import { IdentityProvidersPanel } from "./IdentityProvidersPanel";
import { TagsPanel } from "./TagsPanel";
import { StorageIntegrationPanel } from "./StorageIntegrationPanel";
import { SMTPConfigPanel } from "./SMTPConfigPanel";
import { SlackIntegrationPanel } from "./SlackIntegrationPanel";
import { OnCallSchedulesListPage } from "./OnCallSchedulesListPage";
import { OnCallScheduleDetailPage } from "./OnCallScheduleDetailPage";
import { IncidentSLAPanel } from "./IncidentSLAPanel";
import { EscalationPoliciesPanel } from "./EscalationPoliciesPanel";
import { AuditExportPanel } from "./AuditExportPanel";
import { AdminAuditLogPanel } from "./AdminAuditLogPanel";
import { RetentionConfigPanel } from "./RetentionConfigPanel";
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
      { to: "/settings/field-mapping-templates", labelKey: "settings.nav.fieldMappingTemplates" },
      { to: "/settings/ai-integration", labelKey: "settings.nav.aiIntegration" },
      { to: "/settings/mcp-servers", labelKey: "settings.nav.mcpServers" },
      { to: "/settings/storage", labelKey: "settings.nav.storage" },
      { to: "/settings/smtp", labelKey: "settings.nav.smtp" },
    ],
  },
  {
    // Deliberately its own top-level group, not folded into "Integrações"
    // above -- Slack (and future connectors alongside it) is a different
    // kind of integration: a live, bot-driven, bidirectional link into
    // another product, not a one-way data sink/source config like
    // webhooks/storage/SMTP. See the Slack integration foundation plan for
    // why this distinction was called out explicitly.
    labelKey: "settings.navGroups.connectors",
    items: [{ to: "/settings/integrations/slack", labelKey: "settings.nav.slack" }],
  },
  {
    labelKey: "settings.navGroups.identityAccess",
    items: [
      { to: "/settings/users", labelKey: "settings.nav.users" },
      { to: "/settings/roles", labelKey: "settings.nav.roles" },
      { to: "/settings/identity-providers", labelKey: "settings.nav.identityProviders" },
      { to: "/settings/tags", labelKey: "settings.nav.tags" },
    ],
  },
  {
    labelKey: "settings.navGroups.operations",
    items: [
      { to: "/settings/on-call-schedules", labelKey: "settings.nav.onCallSchedule" },
      { to: "/settings/incident-sla", labelKey: "settings.nav.incidentSla" },
      { to: "/settings/escalation-policies", labelKey: "settings.nav.escalationPolicies" },
    ],
  },
  {
    labelKey: "settings.navGroups.dataAudit",
    items: [
      { to: "/settings/audit-log", labelKey: "settings.nav.auditLog" },
      { to: "/settings/audit-export", labelKey: "settings.nav.auditExport" },
      { to: "/settings/retention", labelKey: "settings.nav.retention" },
      { to: "/settings/database-migration", labelKey: "settings.nav.databaseMigration" },
    ],
  },
] as const;

// Which group labelKeys are collapsed, persisted across reloads -- same
// "small standalone localStorage read/write, no cross-component event
// needed" shape as theme.ts, just without onThemeChange's pub/sub since
// only SettingsLayout itself ever reads this. Storing collapsed (not
// expanded) keys means an empty/missing/corrupt value reads as "nothing
// collapsed" -- every group starts open the first time this ships, instead
// of an unset default silently hiding the whole nav.
const NAV_COLLAPSED_STORAGE_KEY = "kuruops.settings-nav-collapsed";

function getStoredCollapsedGroups(): Set<string> {
  try {
    const raw = localStorage.getItem(NAV_COLLAPSED_STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? new Set(parsed.filter((v): v is string => typeof v === "string")) : new Set();
  } catch {
    return new Set();
  }
}

function storeCollapsedGroups(groups: Set<string>) {
  try {
    localStorage.setItem(NAV_COLLAPSED_STORAGE_KEY, JSON.stringify([...groups]));
  } catch {
    // Private-browsing/storage-full: the toggle still works for the rest of
    // this session via React state, it just won't survive a reload -- not
    // worth surfacing an error for a purely cosmetic preference.
  }
}

// Scoped fallback for a crash inside one settings panel: the nav one level
// up in SettingsLayout is still rendered fine (it's outside this boundary),
// so this only needs to replace the broken panel itself, not the whole app
// shell the root ErrorBoundary's fallback assumes.
function SettingsPanelErrorFallback() {
  const { t } = useTranslation();
  return (
    <div className="empty-state">
      <p>{t("settings.panelError.message")}</p>
      <button className="btn btn-secondary btn-sm" onClick={() => window.location.reload()}>
        {t("settings.panelError.reload")}
      </button>
    </div>
  );
}

export function SettingsLayout() {
  const { t } = useTranslation();
  const location = useLocation();
  const [navSearch, setNavSearch] = useState("");
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(getStoredCollapsedGroups);

  function toggleGroup(labelKey: string) {
    setCollapsedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(labelKey)) next.delete(labelKey);
      else next.add(labelKey);
      storeCollapsedGroups(next);
      return next;
    });
  }

  // Client-side substring match against each item's own translated label --
  // past ~18 items across 5 groups, scrolling to find one you already know
  // the name of costs more than typing a few letters. Filters items, not
  // whole groups: a group with zero matches just doesn't render (no "no
  // results" state needed, since the panel on the right keeps showing
  // whatever's currently open regardless of what the nav filters down to).
  const query = navSearch.trim().toLowerCase();
  const filteredGroups = query
    ? NAV_GROUPS.map((group) => ({
        ...group,
        items: group.items.filter((item) => t(item.labelKey).toLowerCase().includes(query)),
      })).filter((group) => group.items.length > 0)
    : NAV_GROUPS;

  return (
    <>
      <h1 className="page-title">{t("settings.title")}</h1>
      <p className="page-sub">{t("settings.subtitle")}</p>

      <div className="settings-layout">
        <nav className="settings-nav">
          <input
            className="input input-search"
            style={{ marginBottom: 10 }}
            placeholder={t("settings.navSearchPlaceholder")}
            value={navSearch}
            onChange={(e) => setNavSearch(e.target.value)}
            aria-label={t("settings.navSearchPlaceholder")}
          />
          {filteredGroups.length === 0 && (
            <p className="helper-text" style={{ padding: "0 4px" }}>
              {t("settings.navSearchNoResults")}
            </p>
          )}
          {filteredGroups.map((group) => {
            // The group holding the current page always renders its items,
            // regardless of stored collapse state -- collapsing a category
            // should never hide where you already are. Searching does the
            // same for every group with a match: collapse state is a
            // browsing convenience, not something that should be able to
            // hide a search result.
            const isActiveGroup = group.items.some((item) => location.pathname.startsWith(item.to));
            const expanded = Boolean(query) || isActiveGroup || !collapsedGroups.has(group.labelKey);
            return (
              <div className="settings-nav-group" key={group.labelKey}>
                <button
                  type="button"
                  className="settings-nav-group-label settings-nav-group-toggle"
                  onClick={() => toggleGroup(group.labelKey)}
                  aria-expanded={expanded}
                >
                  {t(group.labelKey)}
                  <ChevronIcon width={13} height={13} className={expanded ? undefined : "settings-nav-group-chevron-collapsed"} />
                </button>
                {expanded &&
                  group.items.map((item) => (
                    <NavLink key={item.to} to={item.to} end>
                      {({ isActive }) => <span data-active={isActive}>{t(item.labelKey)}</span>}
                    </NavLink>
                  ))}
              </div>
            );
          })}
        </nav>

        <div className="settings-panel">
          <ErrorBoundary fallback={<SettingsPanelErrorFallback />}>
            <Routes>
              <Route path="webhooks" element={<WebhooksPanel />} />
              <Route path="field-mapping-templates" element={<FieldMappingTemplatesPanel />} />
              <Route path="ai-integration" element={<LLMProvidersPanel />} />
              <Route path="mcp-servers" element={<MCPServersPanel />} />
              <Route path="tags" element={<TagsPanel />} />
              <Route path="users" element={<UsersPanel />} />
              <Route path="roles" element={<RolesPanel />} />
              <Route path="identity-providers" element={<IdentityProvidersPanel />} />
              <Route path="storage" element={<StorageIntegrationPanel />} />
              <Route path="smtp" element={<SMTPConfigPanel />} />
              <Route path="integrations/slack" element={<SlackIntegrationPanel />} />
              <Route path="on-call-schedules" element={<OnCallSchedulesListPage />} />
              <Route path="on-call-schedules/:id" element={<OnCallScheduleDetailPage />} />
              <Route path="incident-sla" element={<IncidentSLAPanel />} />
              <Route path="escalation-policies" element={<EscalationPoliciesPanel />} />
              <Route path="audit-log" element={<AdminAuditLogPanel />} />
              <Route path="audit-export" element={<AuditExportPanel />} />
              <Route path="retention" element={<RetentionConfigPanel />} />
              <Route path="database-migration" element={<DatabaseMigrationPanel />} />
            </Routes>
          </ErrorBoundary>
        </div>
      </div>
    </>
  );
}
