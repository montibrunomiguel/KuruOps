import { useEffect, useRef, useState } from "react";
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

const LISTBOX_ID = "command-palette-listbox";
const optionId = (id: string) => `command-palette-option-${id}`;

export function CommandPalette() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const dialogRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  // Captured when the palette opens, restored by close() below. A manual focus trap
  // (rather than a headless-UI dependency) is enough here: this dialog has
  // exactly one real tab stop (the input; result rows are virtually
  // navigated via aria-activedescendant, not real focus, per the combobox
  // pattern), so "trap" mostly means "don't let focus silently drift onto
  // the page behind the modal backdrop."
  const previouslyFocused = useRef<HTMLElement | null>(null);

  const filteredCommands = COMMANDS.filter((cmd) => {
    const label = t(cmd.labelKey).toLowerCase();
    return label.includes(query.toLowerCase()) || cmd.id.includes(query.toLowerCase());
  });

  // close restores focus to whatever had it before the palette opened,
  // called synchronously (before the setIsOpen state update is flushed) so
  // the previously-focused element already holds focus at the moment React
  // removes the dialog's own input from the DOM -- doing this the other way
  // around (restoring focus from an effect that runs after the unmount) is
  // a race: jsdom/browsers auto-blur-to-body when the currently-focused
  // element is removed, which can clobber a focus() call that happens too
  // late.
  function close() {
    const target = previouslyFocused.current;
    previouslyFocused.current = null;
    target?.focus();
    setIsOpen(false);
    setQuery("");
  }

  function handleSelect(path: string) {
    close();
    navigate(path);
  }

  // Global: only the Cmd/Ctrl+K open shortcut needs to listen at the window
  // level, since it must fire regardless of what currently has focus.
  // Escape/Arrow/Enter used to live here too, but that meant this component
  // intercepted those keys even when focus was somewhere else entirely
  // (e.g. a different modal open above it) -- now scoped to the dialog
  // itself via onKeyDown below.
  useEffect(() => {
    function handleGlobalKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setIsOpen((prev) => {
          const next = !prev;
          if (next) previouslyFocused.current = document.activeElement as HTMLElement | null;
          return next;
        });
      }
    }
    window.addEventListener("keydown", handleGlobalKeyDown);
    return () => window.removeEventListener("keydown", handleGlobalKeyDown);
  }, []);

  function handleDialogKeyDown(e: React.KeyboardEvent<HTMLDivElement>) {
    if (e.key === "Escape") {
      close();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIndex((prev) => (filteredCommands.length === 0 ? 0 : (prev + 1) % filteredCommands.length));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIndex((prev) =>
        filteredCommands.length === 0 ? 0 : (prev - 1 + filteredCommands.length) % filteredCommands.length,
      );
    } else if (e.key === "Enter") {
      const selected = filteredCommands[selectedIndex];
      if (selected) {
        e.preventDefault();
        handleSelect(selected.path);
      }
    } else if (e.key === "Tab") {
      // Minimal focus trap: this dialog has exactly one real tab stop, so
      // Tab/Shift+Tab just keep focus on it instead of letting it escape to
      // whatever's behind the backdrop.
      e.preventDefault();
      inputRef.current?.focus();
    }
  }

  if (!isOpen) return null;

  const activeOption = filteredCommands[selectedIndex];

  return (
    <div
      role="dialog"
      aria-modal="true"
      ref={dialogRef}
      onKeyDown={handleDialogKeyDown}
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
      onClick={close}
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
        <div style={{ padding: "16px", borderBottom: "1px solid var(--border)" }}>
          <input
            ref={inputRef}
            type="text"
            role="combobox"
            aria-expanded="true"
            aria-controls={LISTBOX_ID}
            aria-activedescendant={activeOption ? optionId(activeOption.id) : undefined}
            aria-autocomplete="list"
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
        <div role="listbox" id={LISTBOX_ID} style={{ maxHeight: "320px", overflowY: "auto", padding: "8px" }}>
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
                  id={optionId(cmd.id)}
                  role="option"
                  aria-selected={isSelected}
                  tabIndex={-1}
                  onClick={() => handleSelect(cmd.path)}
                  onMouseEnter={() => setSelectedIndex(idx)}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "12px",
                    padding: "10px 12px",
                    borderRadius: "6px",
                    cursor: "pointer",
                    backgroundColor: isSelected ? "var(--surface-hover)" : "transparent",
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
