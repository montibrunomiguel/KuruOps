import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { useList } from "../api/hooks";
import { useEventStream } from "../api/eventStream";
import type { Alert } from "../types/alerts";
import type { Incident } from "../types/incidents";

// Powers the little red counters next to "Alerts"/"Incidents" in the
// sidebar (see design handoff: nav badge counts for open alerts / active
// incidents). canAlerts/canIncidents mirror useAuth().hasResourceAccess --
// the nav item itself is already hidden without the capability (see
// Sidebar.tsx), so skipping the fetch here just avoids a guaranteed 403.
//
// Built on useList (react-query) + useEventStream, the same data-fetching
// pattern every other list/detail page in the app uses -- this hook used to
// be the one holdout still hand-rolling its own useEffect+fetch+cancelled-
// flag pair, with no live-update subscription at all (the counters only
// ever changed on a full page navigation, unlike every list page's own
// counts, which refresh the instant another analyst's change arrives over
// SSE).
export function useSidebarCounts(canAlerts: boolean, canIncidents: boolean) {
  const { isAuthenticated } = useAuth();

  const { data: openAlerts, reload: reloadAlerts } = useList<Alert>(
    ["sidebar-open-alerts"],
    (token) => api.get<Alert[]>("/api/v1/alerts?status=open", token),
    { enabled: isAuthenticated && canAlerts },
  );
  const { data: incidents, reload: reloadIncidents } = useList<Incident>(
    ["sidebar-active-incidents"],
    (token) => api.get<Incident[]>("/api/v1/incidents", token),
    { enabled: isAuthenticated && canIncidents },
  );

  useEventStream((event) => {
    if (event.type === "alert") reloadAlerts();
    if (event.type === "incident") reloadIncidents();
  });

  return {
    openAlerts: canAlerts ? (openAlerts?.length ?? null) : null,
    activeIncidents: canIncidents ? (incidents?.filter((i) => i.phase !== "post_incident").length ?? null) : null,
  };
}
