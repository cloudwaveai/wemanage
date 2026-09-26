import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
} from "recharts";
import { api, ApiError } from "../lib/api";
import type { UsageSummary, QuotaResp } from "../lib/types";
import {
  Card,
  PageHeader,
  StatTile,
  Loading,
  ErrorBox,
  BarList,
  StatusBadge,
  Meter,
} from "../components/ui";
import { fmtNum, fmtDayShort, fmtDateTime } from "../lib/format";

const RANGES = [
  { v: 1, label: "今天" },
  { v: 7, label: "7 天" },
  { v: 30, label: "30 天" },
];

export function UsagePage() {
  const [range, setRange] = useState(7);

  const usage = useQuery({
    queryKey: ["my-usage", range],
    queryFn: () => api.get<UsageSummary>(`/api/me/usage?range=${range}`),
  });

  const quota = useQuery({
    queryKey: ["my-quota"],
    queryFn: async () => {
      try {
        return await api.get<QuotaResp>("/api/me/quota");
      } catch (e) {
        // 404 = no account bound; treat as "no quota" rather than an error.
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    retry: false,
  });

  const u = usage.data;
  const totalTokens = u
    ? u.tokens_input + u.tokens_output + u.tokens_reasoning
    : 0;

  const daily = (u?.daily ?? []).map((p) => ({
    day: fmtDayShort(p.day),
    requests: p.requests,
  }));

  return (
    <div>
      <PageHeader
        title="我的用量"
        desc="你的请求量与 token 消耗统计。"
        right={
          <div className="inline-flex rounded-lg border border-slate-300 bg-white p-0.5">
            {RANGES.map((r) => (
              <button
                key={r.v}
                onClick={() => setRange(r.v)}
                className={
                  "rounded-md px-3 py-1.5 text-sm font-medium transition-colors " +
                  (range === r.v
                    ? "bg-indigo-600 text-white"
                    : "text-slate-600 hover:bg-slate-100")
                }
              >
                {r.label}
              </button>
            ))}
          </div>
        }
      />

      {usage.isLoading && <Loading />}
      {usage.error && <ErrorBox error={usage.error} />}

      {u && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <StatTile label="请求数" value={fmtNum(u.requests)} />
            <StatTile label="总 Token" value={fmtNum(totalTokens)} />
            <StatTile label="输入 Token" value={fmtNum(u.tokens_input)} />
            <StatTile label="输出 Token" value={fmtNum(u.tokens_output)} />
          </div>

          {/* quota */}
          {quota.data && (
            <Card className="p-5">
              <div className="mb-3 flex items-center justify-between">
                <h2 className="text-sm font-semibold text-slate-800">账号额度</h2>
                <StatusBadge status={quota.data.status} />
              </div>
              {quota.data.quota ? (
                <div className="grid gap-4 sm:grid-cols-2">
                  <Meter
                    label="主额度已用"
                    pct={quota.data.quota.PrimaryUsedPct}
                    hint={
                      quota.data.quota.PrimaryResetAt
                        ? `重置于 ${fmtDateTime(quota.data.quota.PrimaryResetAt)}`
                        : undefined
                    }
                  />
                  <Meter
                    label="次额度已用"
                    pct={quota.data.quota.SecondaryUsedPct}
                    hint={
                      quota.data.quota.SecondaryResetAt
                        ? `重置于 ${fmtDateTime(quota.data.quota.SecondaryResetAt)}`
                        : undefined
                    }
                  />
                </div>
              ) : (
                <p className="text-sm text-slate-400">暂无额度快照。</p>
              )}
            </Card>
          )}

          {/* daily line chart */}
          <Card className="p-5">
            <h2 className="mb-3 text-sm font-semibold text-slate-800">
              每日请求趋势
            </h2>
            {daily.length ? (
              <div className="h-64 w-full">
                <ResponsiveContainer width="100%" height="100%">
                  <LineChart
                    data={daily}
                    margin={{ top: 8, right: 12, bottom: 4, left: -12 }}
                  >
                    <CartesianGrid strokeDasharray="3 3" stroke="#eef1f6" />
                    <XAxis
                      dataKey="day"
                      tick={{ fontSize: 12, fill: "#94a3b8" }}
                      tickLine={false}
                      axisLine={{ stroke: "#e2e8f0" }}
                    />
                    <YAxis
                      tick={{ fontSize: 12, fill: "#94a3b8" }}
                      tickLine={false}
                      axisLine={false}
                      allowDecimals={false}
                    />
                    <Tooltip
                      formatter={(v: number) => [fmtNum(v), "请求数"]}
                      contentStyle={{
                        borderRadius: 8,
                        border: "1px solid #e2e8f0",
                        fontSize: 12,
                      }}
                    />
                    <Line
                      type="monotone"
                      dataKey="requests"
                      stroke="#4f46e5"
                      strokeWidth={2}
                      dot={{ r: 2.5 }}
                      activeDot={{ r: 4 }}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </div>
            ) : (
              <p className="py-8 text-center text-sm text-slate-400">
                该时间范围内暂无请求。
              </p>
            )}
          </Card>

          {/* breakdowns */}
          <div className="grid gap-4 md:grid-cols-2">
            <Card className="p-5">
              <h2 className="mb-3 text-sm font-semibold text-slate-800">
                按模型
              </h2>
              <BarList
                items={(u.by_model ?? []).map((b) => ({
                  label: b.key,
                  value: b.requests,
                  sub: `${fmtNum(b.requests)} 次 · ${fmtNum(b.tokens_total)} tok`,
                }))}
              />
            </Card>
            <Card className="p-5">
              <h2 className="mb-3 text-sm font-semibold text-slate-800">
                按客户端
              </h2>
              <BarList
                items={(u.by_client ?? []).map((b) => ({
                  label: b.key,
                  value: b.requests,
                  sub: `${fmtNum(b.requests)} 次 · ${fmtNum(b.tokens_total)} tok`,
                }))}
              />
            </Card>
          </div>
        </div>
      )}
    </div>
  );
}
