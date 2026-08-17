import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { IncidentTrendPoint } from "../../types/dashboard";
import { currentLocale } from "../../lib/format";

// Plain bar chart: incident volume per day -- the Dashboard Incidents tab's
// counterpart of TrendChart, without a combined MTTR line since incident
// MTTA/MTTR are already shown tenant-wide as a KPI card, not per-day.
export function IncidentTrendChart({ points }: { points: IncidentTrendPoint[] }) {
  const data = points.map((p) => ({
    day: new Date(p.day + "T00:00:00").toLocaleDateString(currentLocale(), { day: "2-digit", month: "2-digit" }),
    incidentCount: p.incidentCount,
  }));

  return (
    <ResponsiveContainer width="100%" height={220}>
      <BarChart data={data} margin={{ top: 8, right: 8, left: -20, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
        <XAxis dataKey="day" tick={{ fill: "var(--text-muted)", fontSize: 11 }} axisLine={{ stroke: "var(--border)" }} tickLine={false} />
        <YAxis tick={{ fill: "var(--text-muted)", fontSize: 11 }} axisLine={false} tickLine={false} allowDecimals={false} />
        <Tooltip
          contentStyle={{ background: "var(--bg-elevated)", border: "1px solid var(--border-strong)", borderRadius: 8, fontSize: 12 }}
          labelStyle={{ color: "var(--text)" }}
        />
        <Bar dataKey="incidentCount" fill="var(--accent)" radius={[4, 4, 0, 0]} maxBarSize={36} />
      </BarChart>
    </ResponsiveContainer>
  );
}
