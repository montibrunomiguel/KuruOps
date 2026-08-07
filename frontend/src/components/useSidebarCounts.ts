import { useEffect, useState } from "react";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import type { Alert } from "../types/alerts";
import type { Incident } from "../types/incidents";

// Powers the little red counters next to "Alerts"/"Incidents" in the
// sidebar (see design handoff: nav badge counts for open alerts / active
// incidents). canAlerts/canIncidents mirror useAuth().hasResourceAccess --
// the nav item itself is already hidden without the capability (see
// Sidebar.tsx), so skipping the fetch here just avoids a guaranteed 403.
export function useSidebarCounts(canAlerts: boolean, canIncidents: boolean) {
  const { token, isAuthenticated } = useAuth();
  const [openAlerts, setOpenAlerts] = useState<number | null>(null);
  const [activeIncidents, setActiveIncidents] = useState<number | null>(null);

  useEffect(() => {
    if (!isAuthenticated || !canAlerts) return;
    let cancelled = false;

    api
      .get<Alert[]>("/api/v1/alerts?status=open", token)
      .then((rows) => !cancelled && setOpenAlerts(rows?.length ?? 0))
      .catch(() => !cancelled && setOpenAlerts(null));

    return () => {
      cancelled = true;
    };
  }, [token, isAuthenticated, canAlerts]);

  useEffect(() => {
    if (!isAuthenticated || !canIncidents) return;
    let cancelled = false;

    api
      .get<Incident[]>("/api/v1/incidents", token)
      .then((rows) => !cancelled && setActiveIncidents((rows ?? []).filter((i) => i.phase !== "post_incident").length))
      .catch(() => !cancelled && setActiveIncidents(null));

    return () => {
      cancelled = true;
    };
  }, [token, isAuthenticated, canIncidents]);

  return { openAlerts, activeIncidents };
}
