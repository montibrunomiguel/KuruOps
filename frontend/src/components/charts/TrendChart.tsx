import { Bar, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useTranslation } from "react-i18next";
import type { AlertTrendPoint } from "../../types/dashboard";

// Bar+line combo: alert volume per day (bars, left axis) against MTTR in
// hours (dots/line, right axis) -- the Dashboard Alerts tab's "Alert Volume
// & MTTR Trend" chart. avgMttrSeconds is nullable per point (a day with no
// closed alerts has nothing to average), so the line only plots where data
// exists rather than drawing a misleading zero.
export function TrendChart({ points }: { points: AlertTrendPoint[] }) {
  const { i18n } = useTranslation();
  const locale = i18n.language === "en" ? "en-US" : "pt-BR";

  const data = points.map((p) => ({
    day: new Date(p.day + "T00:00:00").toLocaleDateString(locale, { weekday: "narrow" }),
    alertCount: p.alertCount,
    mttrHours: p.avgMttrSeconds != null ? Math.round((p.avgMttrSeconds / 3600) * 10) / 10 : undefined,
  }));

  return (
    <ResponsiveContainer width="100%" height={220}>
      <ComposedChart data={data} margin={{ top: 8, right: 8, left: -20, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
        <XAxis dataKey="day" tick={{ fill: "var(--text-muted)", fontSize: 11 }} axisLine={{ stroke: "var(--border)" }} tickLine={false} />
        <YAxis yAxisId="left" tick={{ fill: "var(--text-muted)", fontSize: 11 }} axisLine={false} tickLine={false} allowDecimals={false} />
        <YAxis yAxisId="right" orientation="right" tick={{ fill: "var(--text-muted)", fontSize: 11 }} axisLine={false} tickLine={false} hide />
        <Tooltip
          contentStyle={{ background: "var(--bg-elevated)", border: "1px solid var(--border-strong)", borderRadius: 8, fontSize: 12 }}
          labelStyle={{ color: "var(--text)" }}
        />
        <Bar yAxisId="left" dataKey="alertCount" fill="var(--accent)" radius={[4, 4, 0, 0]} maxBarSize={36} />
        <Line
          yAxisId="right"
          type="monotone"
          dataKey="mttrHours"
          stroke="var(--success)"
          strokeWidth={0}
          dot={{ r: 4, fill: "var(--success)", strokeWidth: 0 }}
          connectNulls
        />
      </ComposedChart>
    </ResponsiveContainer>
  );
}
