import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { AccountRow, Overview } from "../lib/types";
import {
  Card,
  PageHeader,
  StatTile,
  Loading,
  ErrorBox,
  StatusBadge,
  Empty,
  Meter,
} from "../components/ui";
import { fmtNum, fmtCompact, fmtDateTime } from "../lib/format";

export function OverviewPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["overview"],
    queryFn: () => api.get<Overview>("/api/admin/overview"),
  });

  const u = data?.usage_30d;
  const totalTokens = u
    ? u.tokens_input + u.tokens_cached + u.tokens_output + u.tokens_reasoning
    : 0;
  const accounts = useQuery({
    queryKey: ["accounts"],
    queryFn: () => api.get<{ accounts: AccountRow[] }>("/api/admin/accounts"),
  });
  const health = data?.account_health ?? {};
  const alerts = data?.alerts ?? [];

  return (
    <div>
      <PageHeader title="总览" desc="网关近 30 天运行概况。" />

      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}

      {data && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
            <StatTile label="30 天请求数" value={fmtNum(u?.requests ?? 0)} />
            <StatTile label="30 天总 Token" value={fmtNum(totalTokens)} />
            <StatTile label="24h 活跃人数" value={fmtNum(data.active_24h)} />
            <StatTile label="账号总数" value={fmtNum(data.account_total)} />
          </div>

          <AccountQuotaCard rows={accounts.data?.accounts ?? []} />

          <div className="grid gap-4 lg:grid-cols-2">
            <Card className="p-5">
              <h2 className="mb-3 text-sm font-semibold text-slate-800">
                账号健康度
              </h2>
              {Object.keys(health).length ? (
                <div className="flex flex-wrap gap-2">
                  {Object.entries(health).map(([status, count]) => (
                    <div
                      key={status}
                      className="flex items-center gap-2 rounded-lg border border-slate-200 px-3 py-2"
                    >
                      <StatusBadge status={status} />
                      <span className="text-lg font-semibold text-slate-800">
                        {fmtNum(count)}
                      </span>
                    </div>
                  ))}
                </div>
              ) : (
                <Empty label="暂无账号" />
              )}
            </Card>

            <Card className="p-5">
              <h2 className="mb-3 text-sm font-semibold text-slate-800">
                告警
                <span className="ml-2 text-xs font-normal text-slate-400">
                  {alerts.length} 条
                </span>
              </h2>
              {alerts.length ? (
                <div className="scroll-thin max-h-72 space-y-2 overflow-y-auto">
                  {alerts.map((a, i) => (
                    <div
                      key={i}
                      className="flex items-center justify-between rounded-lg bg-rose-50 px-3 py-2 text-sm"
                    >
                      <div>
                        <div className="font-medium text-slate-800">
                          {a.account_email}
                        </div>
                        <div className="text-xs text-rose-600">{a.kind}</div>
                      </div>
                      <div className="text-xs text-slate-400">
                        {fmtDateTime(a.at)}
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <Empty label="暂无告警" />
              )}
            </Card>
          </div>
        </div>
      )}
    </div>
  );
}

// resetHint describes when a window resets, or that the reading is outdated.
function resetHint(resetAt: string | null | undefined): string | undefined {
  if (!resetAt) return undefined;
  return new Date(resetAt).getTime() < Date.now()
    ? "已过重置时间，等待下次更新"
    : `${fmtDateTime(resetAt)} 重置`;
}

// AccountQuotaCard shows every account's 5-hour / weekly quota and tokens.
function AccountQuotaCard({ rows }: { rows: AccountRow[] }) {
  return (
    <Card className="p-5">
      <h2 className="mb-1 text-sm font-semibold text-slate-800">账号额度与用量</h2>
      <p className="mb-4 text-xs text-slate-400">
        5 小时 / 每周额度来自 ChatGPT、Kimi 官方接口；Token 为经过网关的用量。详情和手动刷新在「账号」页。
      </p>
      {rows.length === 0 ? (
        <Empty label="暂无账号" />
      ) : (
        <div className="divide-y divide-slate-100">
          {rows.map((a) => {
            const quota = !a.provider || a.provider === "codex" || a.provider === "kimi";
            return (
              <div
                key={a.id}
                className="grid items-center gap-4 py-3 sm:grid-cols-[minmax(0,1.2fr)_1fr_1fr_minmax(0,1fr)]"
              >
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium text-slate-800">
                    {a.email || (a.provider === "kimi" ? "Kimi Code" : `账号 #${a.id}`)}
                    {a.provider === "kimi" && (
                      <span className="ml-1.5 rounded bg-violet-100 px-1.5 py-0.5 text-[10px] font-medium text-violet-700">
                        Kimi
                      </span>
                    )}
                  </div>
                  <div className="mt-0.5 flex items-center gap-1.5 text-xs text-slate-400">
                    <StatusBadge status={a.status} />
                    <span className="truncate">{a.employee_name || "未绑定"}</span>
                  </div>
                </div>
                {quota ? (
                  <>
                    <Meter
                      label="5 小时"
                      pct={a.quota?.primary_used_pct}
                      hint={resetHint(a.quota?.primary_reset_at)}
                    />
                    <Meter
                      label="每周"
                      pct={a.quota?.secondary_used_pct}
                      hint={resetHint(a.quota?.secondary_reset_at)}
                    />
                  </>
                ) : (
                  <div className="text-xs text-slate-400 sm:col-span-2">无额度窗口</div>
                )}
                <div className="text-right text-xs text-slate-500 tabular-nums">
                  <div>
                    24h <span className="font-medium text-slate-700">{fmtCompact(a.usage.tokens_24h)}</span>
                    {" · "}7 天 <span className="font-medium text-slate-700">{fmtCompact(a.usage.tokens_7d)}</span>
                  </div>
                  <div className="mt-0.5 text-slate-400">
                    累计 {fmtCompact(a.usage.tokens_all)} Token · {fmtNum(a.usage.requests_all)} 次
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </Card>
  );
}
