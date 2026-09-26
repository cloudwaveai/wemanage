import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { Overview } from "../lib/types";
import {
  Card,
  PageHeader,
  StatTile,
  Loading,
  ErrorBox,
  StatusBadge,
  Empty,
} from "../components/ui";
import { fmtNum, fmtDateTime } from "../lib/format";

export function OverviewPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["overview"],
    queryFn: () => api.get<Overview>("/api/admin/overview"),
  });

  const u = data?.usage_30d;
  const totalTokens = u
    ? u.tokens_input + u.tokens_cached + u.tokens_output + u.tokens_reasoning
    : 0;
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
