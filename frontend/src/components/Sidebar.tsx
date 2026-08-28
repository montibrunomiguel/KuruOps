import { useEffect, useState } from "react";
import { NavLink } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { useSidebarCounts } from "./useSidebarCounts";
import {
  DashboardIcon,
  AlertIcon,
  ShieldIcon,
  PlaybookIcon,
  GearIcon,
  MoonIcon,
  SunIcon,
  GlobeIcon,
  MenuIcon,
  CloseIcon,
} from "./icons";
import { BrandMark } from "./BrandMark";
import { applyTheme, getStoredTheme, type Theme } from "../theme";
import { setLanguage } from "../i18n";
import { initials } from "../lib/format";

export function Sidebar() {
  const { t, i18n } = useTranslation();
  const { user, isAdmin, hasResourceAccess, logout } = useAuth();
  const canAlerts = hasResourceAccess("alerts");
  const canIncidents = hasResourceAccess("incidents");
  const canFollowup = hasResourceAccess("followup");
  const { openAlerts, activeIncidents } = useSidebarCounts(canAlerts, canIncidents);
  const [theme, setTheme] = useState<Theme>(getStoredTheme());
  const [open, setOpen] = useState(false);

  function toggleTheme() {
    const next: Theme = theme === "dark" ? "light" : "dark";
    setTheme(next);
    applyTheme(next);
  }

  // Below the tablet breakpoint the sidebar becomes an off-canvas drawer
  // (see components.css); Escape closes it the same way it closes the
  // command palette, for the same reason -- it's the standard way to
  // dismiss an overlay without hunting for a close button.
  useEffect(() => {
    if (!open) return;
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [open]);

  return (
    <>
      <button
        type="button"
        className="sidebar-toggle"
        onClick={() => setOpen(true)}
        aria-label={t("sidebar.openMenu")}
        aria-expanded={open}
      >
        <MenuIcon width={20} height={20} />
      </button>
      {open && <div className="sidebar-backdrop" onClick={() => setOpen(false)} />}
      <aside className="sidebar" data-open={open}>
        <div className="sidebar-brand">
          <BrandMark />
          <div>
            <div className="sidebar-brand-title">KuruOps</div>
            <div className="sidebar-brand-sub">{t("sidebar.brandSub")}</div>
          </div>
          <button
            type="button"
            className="sidebar-close"
            onClick={() => setOpen(false)}
            aria-label={t("sidebar.closeMenu")}
          >
            <CloseIcon width={18} height={18} />
          </button>
        </div>

        <nav className="sidebar-nav" onClick={() => setOpen(false)}>
          {/* Every Dashboard tab is individually gated (see DashboardLayout);
              the nav link itself only needs to disappear when none of the
              three capabilities are granted at all. */}
          {(canAlerts || canIncidents || canFollowup) && (
            <SidebarLink to="/dashboard" icon={<DashboardIcon />} label={t("sidebar.nav.dashboard")} />
          )}
          {canAlerts && (
            <SidebarLink
              to="/alerts"
              icon={<AlertIcon />}
              label={t("sidebar.nav.alerts")}
              count={openAlerts}
              countTone="critical"
            />
          )}
          {canIncidents && (
            <SidebarLink
              to="/incidents"
              icon={<ShieldIcon />}
              label={t("sidebar.nav.incidents")}
              count={activeIncidents}
              countTone="critical"
            />
          )}
          <SidebarLink to="/playbooks" icon={<PlaybookIcon />} label={t("sidebar.nav.playbooks")} />
          {isAdmin && <SidebarLink to="/settings/webhooks" icon={<GearIcon />} label={t("sidebar.nav.settings")} />}
        </nav>

        <div className="sidebar-footer">
          <button
            type="button"
            className="sidebar-theme-toggle"
            onClick={toggleTheme}
            aria-label={theme === "dark" ? t("sidebar.lightMode") : t("sidebar.darkMode")}
          >
            {theme === "dark" ? <MoonIcon width={16} height={16} /> : <SunIcon width={16} height={16} />}
            <span>{theme === "dark" ? t("sidebar.darkMode") : t("sidebar.lightMode")}</span>
          </button>

          <div className="sidebar-lang-toggle" role="group" aria-label={t("sidebar.language")}>
            <GlobeIcon width={14} height={14} />
            <button
              type="button"
              data-active={i18n.language === "en"}
              onClick={() => setLanguage("en")}
            >
              EN
            </button>
            <button
              type="button"
              data-active={i18n.language === "pt"}
              onClick={() => setLanguage("pt")}
            >
              PT
            </button>
          </div>

          <NavLink to="/profile" className="sidebar-user" style={{ textDecoration: "none", color: "inherit" }}>
            <div className="sidebar-user-avatar">{initials(user?.name)}</div>
            <div className="sidebar-user-info">
              <div className="sidebar-user-name">{user?.name}</div>
              <div className="sidebar-user-role">{user?.role}</div>
            </div>
          </NavLink>
          <button className="btn btn-ghost btn-sm" style={{ width: "100%", marginTop: 8 }} onClick={logout}>
            {t("sidebar.logout")}
          </button>
        </div>
      </aside>
    </>
  );
}

function SidebarLink({
  to,
  icon,
  label,
  count,
  countTone,
}: {
  to: string;
  icon: React.ReactNode;
  label: string;
  count?: number | null;
  countTone?: "critical";
}) {
  return (
    <NavLink to={to} className="sidebar-link">
      {({ isActive }: { isActive: boolean }) => (
        <span className="sidebar-link-inner" data-active={isActive}>
          <span className="sidebar-link-icon">{icon}</span>
          <span className="sidebar-link-label">{label}</span>
          {!!count && (
            <span className={`sidebar-link-count ${countTone === "critical" ? "sidebar-link-count-critical" : ""}`}>
              {count}
            </span>
          )}
        </span>
      )}
    </NavLink>
  );
}
