import { useId, useState } from "react";
import type { Alert } from "../types/alerts";
import { shortId } from "../lib/format";

// Search-by-title/id/asset/srcIp + click-to-link, shared by AlertDetailPage's
// LinkedAlertsPanel (alert-to-alert correlation) and IncidentDetailPage's
// correlated-alerts panel (incident-to-alert). A raw "type the alert's ID"
// text field doesn't work in practice: every alert ID shown anywhere in the
// UI is truncated to 8 characters (see lib/format.shortId), so there's never
// a way to discover/copy a full UUID to paste in, and submitting the
// truncated one always fails with "invalid alert id" server-side. Matching
// on asset/srcIp too lets an analyst find related alerts by affected
// host/IP without already knowing the other alert's title. excludeIds is
// the caller's responsibility -- already-linked alerts, plus the current
// entity's own id when linking alert-to-alert (an alert can't link itself).
//
// Keyboard-accessible combobox: same role="combobox"/aria-activedescendant/
// arrow-key pattern CommandPalette.tsx already proved out, scaled down to
// this component's simpler inline (non-modal) shape -- there's no dialog to
// focus-trap here, so it's just the input's own onKeyDown plus a listbox of
// virtually-navigated options, not real focus, per the standard combobox
// pattern (a screen reader announces the active option via
// aria-activedescendant without focus ever leaving the input).
export function LinkSearchPicker({
  candidates,
  excludeIds,
  onLink,
  placeholder,
}: {
  candidates: Alert[];
  excludeIds: Set<string>;
  onLink: (id: string) => void;
  placeholder: string;
}) {
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const listboxId = useId();
  const optionId = (id: string) => `${listboxId}-option-${id}`;

  const results = candidates.filter((a) => {
    if (excludeIds.has(a.id) || query.length === 0) return false;
    const q = query.toLowerCase();
    return (
      a.id.toLowerCase().includes(q) ||
      a.title.toLowerCase().includes(q) ||
      (a.asset ?? "").toLowerCase().includes(q) ||
      (a.srcIp ?? "").toLowerCase().includes(q)
    );
  });

  function handleLink(id: string) {
    setQuery("");
    setActiveIndex(0);
    onLink(id);
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (results.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActiveIndex((prev) => (prev + 1) % results.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActiveIndex((prev) => (prev - 1 + results.length) % results.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const active = results[activeIndex];
      if (active) handleLink(active.id);
    } else if (e.key === "Escape") {
      setQuery("");
      setActiveIndex(0);
    }
  }

  const activeOption = results[activeIndex];

  return (
    <div>
      <input
        className="input"
        style={{ width: "100%" }}
        placeholder={placeholder}
        value={query}
        role="combobox"
        aria-expanded={results.length > 0}
        aria-controls={listboxId}
        aria-autocomplete="list"
        aria-activedescendant={activeOption ? optionId(activeOption.id) : undefined}
        onChange={(e) => {
          setQuery(e.target.value);
          setActiveIndex(0);
        }}
        onKeyDown={handleKeyDown}
      />
      {results.length > 0 && (
        <div className="search-result-list" role="listbox" id={listboxId}>
          {results.map((a, idx) => (
            <div
              className={`search-result-item${idx === activeIndex ? " active" : ""}`}
              key={a.id}
              id={optionId(a.id)}
              role="option"
              aria-selected={idx === activeIndex}
              tabIndex={-1}
              onClick={() => handleLink(a.id)}
              onMouseEnter={() => setActiveIndex(idx)}
            >
              <span className="mono">{shortId(a.id)}</span> · {a.title}
              {a.asset && <span className="helper-text"> · {a.asset}</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
