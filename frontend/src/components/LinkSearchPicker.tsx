import { useState } from "react";
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
    onLink(id);
  }

  return (
    <div>
      <input
        className="input"
        style={{ width: "100%" }}
        placeholder={placeholder}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />
      {results.length > 0 && (
        <div className="search-result-list">
          {results.map((a) => (
            <div className="search-result-item" key={a.id} onClick={() => handleLink(a.id)}>
              <span className="mono">{shortId(a.id)}</span> · {a.title}
              {a.asset && <span className="helper-text"> · {a.asset}</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
