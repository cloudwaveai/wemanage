import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { UsageSeries } from "../lib/types";
import { fmtNum } from "../lib/format";

const pad = (n: number) => String(n).padStart(2, "0");

function bucketLabel(sec: number): string {
  if (sec >= 86400) return "每天";
  if (sec >= 3600) return "每小时";
  return `每 ${Math.round(sec / 60)} 分钟`;
}

// UsageTrend draws requests over time at the resolution the server picked
// for the data (minutes for a busy hour, hours for a few days, days beyond).
export function UsageTrend({ series }: { series?: UsageSeries }) {
  const points = series?.points ?? [];
  const bucket = series?.bucket_seconds ?? 0;
  if (!points.length || !bucket) {
    return (
      <p className="py-8 text-center text-sm text-slate-400">该时间范围内暂无请求。</p>
    );
  }
  const first = new Date(points[0].t);
  const last = new Date(points[points.length - 1].t);
  const multiDay = first.toDateString() !== last.toDateString();
  const tick = (ms: number) => {
    const d = new Date(ms);
    const md = `${pad(d.getMonth() + 1)}/${pad(d.getDate())}`;
    const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
    if (bucket >= 86400) return md;
    return multiDay && d.getHours() === 0 && d.getMinutes() === 0 ? md : hm;
  };
  const full = (ms: number) => {
    const d = new Date(ms);
    const md = `${pad(d.getMonth() + 1)}/${pad(d.getDate())}`;
    return bucket >= 86400 ? md : `${md} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  };
  const data = points.map((p) => ({ ts: new Date(p.t).getTime(), requests: p.requests, tokens: p.tokens }));

  return (
    <div>
      <div className="mb-2 text-xs text-slate-400">{bucketLabel(bucket)}</div>
      <div className="h-64 w-full">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ top: 8, right: 12, bottom: 4, left: -12 }}>
            <defs>
              <linearGradient id="trendFill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="#6366f1" stopOpacity={0.28} />
                <stop offset="100%" stopColor="#6366f1" stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="#eef1f6" vertical={false} />
            <XAxis
              dataKey="ts"
              type="number"
              scale="time"
              domain={["dataMin", "dataMax"]}
              tickFormatter={tick}
              tick={{ fontSize: 12, fill: "#94a3b8" }}
              tickLine={false}
              axisLine={{ stroke: "#e2e8f0" }}
              minTickGap={28}
            />
            <YAxis
              tick={{ fontSize: 12, fill: "#94a3b8" }}
              tickLine={false}
              axisLine={false}
              allowDecimals={false}
            />
            <Tooltip
              labelFormatter={(v: number) => full(v)}
              formatter={(v: number, name: string) => [fmtNum(v), name === "requests" ? "请求数" : "Token"]}
              contentStyle={{ borderRadius: 8, border: "1px solid #e2e8f0", fontSize: 12 }}
            />
            <Area
              type="monotone"
              dataKey="requests"
              stroke="#4f46e5"
              strokeWidth={2}
              fill="url(#trendFill)"
              dot={data.length <= 12 ? { r: 2.5 } : false}
              activeDot={{ r: 4 }}
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}
