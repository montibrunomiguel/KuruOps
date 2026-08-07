import { Cell, Pie, PieChart, ResponsiveContainer } from "recharts";

export interface DonutSlice {
  key: string;
  label: string;
  value: number;
  color: string;
}

// Donut with the total count centered inside the ring -- the legend itself
// is rendered by the caller (see DashboardStatusDonut) since it needs its
// own row layout (dot + label + value) matching the design, not recharts'
// built-in <Legend>.
export function DonutChart({ slices, size = 150 }: { slices: DonutSlice[]; size?: number }) {
  const total = slices.reduce((sum, s) => sum + s.value, 0);

  return (
    <div style={{ position: "relative", width: size, height: size, flexShrink: 0 }}>
      <ResponsiveContainer width="100%" height="100%">
        <PieChart>
          <Pie
            data={slices}
            dataKey="value"
            nameKey="label"
            innerRadius={size * 0.32}
            outerRadius={size * 0.5}
            paddingAngle={slices.length > 1 ? 2 : 0}
            stroke="none"
          >
            {slices.map((s) => (
              <Cell key={s.key} fill={s.color} />
            ))}
          </Pie>
        </PieChart>
      </ResponsiveContainer>
      <div
        style={{
          position: "absolute",
          inset: 0,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          pointerEvents: "none",
        }}
      >
        <div style={{ fontSize: size * 0.24, fontWeight: 700, fontFamily: "IBM Plex Mono, monospace", lineHeight: 1 }}>
          {total}
        </div>
      </div>
    </div>
  );
}
