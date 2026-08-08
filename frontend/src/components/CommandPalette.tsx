import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  AlertIcon,
  DashboardIcon,
  GearIcon,
  PlaybookIcon,
  ShieldIcon,
  UserIcon,
} from "./icons";

interface CommandItem {
  id: string;
  labelKey: string;
  path: string;
  icon: React.ComponentType<{ className?: string }>;
  category: string;
}

const COMMANDS: CommandItem[] = [
  { id: "dashboard", labelKey: "sidebar.nav.dashboard", path: "/dashboard", icon: DashboardIcon, category: "Navigation" },
  { id: "alerts", labelKey: "sidebar.nav.alerts", path: "/alerts", icon: AlertIcon, category: "Navigation" },
  { id: "incidents", labelKey: "sidebar.nav.incidents", path: "/incidents", icon: ShieldIcon, category: "Navigation" },
  { id: "playbooks", labelKey: "sidebar.nav.playbooks", path: "/playbooks", icon: PlaybookIcon, category: "Navigation" },
  { id: "settings", labelKey: "sidebar.nav.settings", path: "/settings", icon: GearIcon, category: "Settings" },
  { id: "profile", labelKey: "profile.title", path: "/profile", icon: UserIcon, category: "Settings" },
];

export function CommandPalette() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setIsOpen((prev) => !prev);
      } else if (e.key === "Escape" && isOpen) {
        setIsOpen(false);
      }
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen]);

  const filteredCommands = COMMANDS.filter((cmd) => {
    const label = t(cmd.labelKey).toLowerCase();
    return label.includes(query.toLowerCase()) || cmd.id.includes(query.toLowerCase());
  });

  function handleSelect(path: string) {
    setIsOpen(false);
    setQuery("");
    navigate(path);
  }

  if (!isOpen) return null;

  return (
    <div
      role="dialog"
      aria-modal="true"
      style={{
        position: "fixed",
        inset: 0,
        backgroundColor: "rgba(0, 0, 0, 0.6)",
        backdropFilter: "blur(4px)",
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "center",
        paddingTop: "10vh",
        zIndex: 9999,
      }}
      onClick={() => setIsOpen(false)}
    >
      <div
        className="panel"
        style={{
          width: "100%",
          maxWidth: "560px",
          borderRadius: "12px",
          boxShadow: "0 20px 25px -5px rgba(0, 0, 0, 0.5)",
          overflow: "hidden",
          padding: 0,
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ padding: "16px", borderBottom: "1px solid var(--border-color, #2d3748)" }}>
          <input
            type="text"
            placeholder={t("commandPalette.placeholder", "Type a command or search...")}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelectedIndex(0);
            }}
            autoFocus
            style={{
              width: "100%",
              background: "transparent",
              border: "none",
              outline: "none",
              color: "inherit",
              fontSize: "1rem",
            }}
          />
        </div>
        <div style={{ maxHeight: "320px", overflowY: "auto", padding: "8px" }}>
          {filteredCommands.length === 0 ? (
            <div style={{ padding: "16px", textAlign: "center", opacity: 0.6 }}>
              {t("commandPalette.noResults", "No matching commands")}
            </div>
          ) : (
            filteredCommands.map((cmd, idx) => {
              const IconComp = cmd.icon;
              const isSelected = idx === selectedIndex;
              return (
                <div
                  key={cmd.id}
                  onClick={() => handleSelect(cmd.path)}
                  onMouseEnter={() => setSelectedIndex(idx)}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "12px",
                    padding: "10px 12px",
                    borderRadius: "6px",
                    cursor: "pointer",
                    backgroundColor: isSelected ? "var(--hover-bg, rgba(255, 255, 255, 0.08))" : "transparent",
                  }}
                >
                  <IconComp />
                  <span style={{ fontWeight: 500 }}>{t(cmd.labelKey)}</span>
                  <span style={{ marginLeft: "auto", fontSize: "0.75rem", opacity: 0.5 }}>
                    {cmd.category}
                  </span>
                </div>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
}
