import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAuth } from "../context/AuthContext";
import type { UsageSummary, QuotaResp } from "../lib/types";
import { ResetCreditsCard } from "../components/ResetCredits";
import {
  Card,
  PageHeader,
  StatTile,
  Loading,
  ErrorBox,
  BarList,
  StatusBadge,
  Meter,
  RangeTabs,
  Switch,
} from "../components/ui";
import { fmtNum, fmtDateTime } from "../lib/format";
import { UsageTrend } from "../components/UsageTrend";

export function UsagePage() {
  const [range, setRange] = useState(7);
  const { me, refetch } = useAuth();
  const reminder = useMutation({
    mutationFn: (enabled: boolean) => api.post("/api/me/quota-alert", { enabled }),
    onSuccess: () => refetch(),
  });
  const usage = useQuery({
    queryKey: ["my-usage", range],
    queryFn: () => api.get<UsageSummary>(`/api/me/usage?range=${range}`),
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  });

  const quota = useQuery({
    queryKey: ["my-quota"],
    queryFn: () => api.get<QuotaResp>("/api/me/quota"),
    retry: false,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
  });

  const u = usage.data;
  const totalTokens = u
    ? u.tokens_input + u.tokens_output + u.tokens_reasoning
    : 0;


  return (
    <div>
      <PageHeader
        title="我的用量"
        desc="你的请求量与 token 消耗统计。"
        right={<RangeTabs value={range} onChange={setRange} />}
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

          {/* quota: one block per account */}
          {quota.data && quota.data.accounts.length > 0 && (
            <Card className="p-5">
              <div className="mb-3 flex items-center justify-between">
                <h2 className="text-sm font-semibold text-slate-800">账号额度</h2>
                {quota.data.accounts.length === 1 && <StatusBadge status={quota.data.accounts[0].status} />}
              </div>
              {quota.data.accounts.length > 1 && (
                <p className="-mt-1 mb-3 text-xs text-slate-500">
                  你有 {quota.data.accounts.length} 个 AI 账号，网关会自动用额度多的那个；一个用完了会自动换到另一个，不用做什么。
                </p>
              )}
              <div className="space-y-4">
                {quota.data.accounts.map((a) => (
                  <div key={a.id} className={quota.data!.accounts.length > 1 ? "rounded-lg border border-slate-100 p-3" : ""}>
                    {quota.data!.accounts.length > 1 && (
                      <div className="mb-2 flex items-center justify-between gap-2">
                        <span className="truncate text-xs font-medium text-slate-600">{a.email || `账号 #${a.id}`}</span>
                        <StatusBadge status={a.status} />
                      </div>
                    )}
                    {a.quota ? (
                      <div className="grid gap-4 sm:grid-cols-2">
                        <Meter
                          label="5 小时额度已用"
                          pct={a.quota.PrimaryUsedPct}
                          hint={a.quota.PrimaryResetAt ? `重置于 ${fmtDateTime(a.quota.PrimaryResetAt)}` : undefined}
                        />
                        <Meter
                          label="每周额度已用"
                          pct={a.quota.SecondaryUsedPct}
                          hint={a.quota.SecondaryResetAt ? `重置于 ${fmtDateTime(a.quota.SecondaryResetAt)}` : undefined}
                        />
                      </div>
                    ) : (
                      <p className="text-sm text-slate-400">暂无额度快照。</p>
                    )}
                  </div>
                ))}
              </div>
              <div className="mt-4 border-t border-slate-100 pt-3">
                <Switch
                  checked={!!me?.quota_alert}
                  onChange={(v) => reminder.mutate(v)}
                  disabled={reminder.isPending}
                  label="额度用到 80% 和用完时，在飞书提醒我（只发给你自己）"
                />
              </div>
            </Card>
          )}

          <ResetCreditsCard />

          <Card className="p-5">
            <h2 className="mb-1 text-sm font-semibold text-slate-800">请求趋势</h2>
            <UsageTrend series={u.series} />
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
