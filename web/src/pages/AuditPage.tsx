import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { AuditRow } from "../lib/types";
import { Card, PageHeader, Loading, ErrorBox, Empty } from "../components/ui";
import { fmtDateTime } from "../lib/format";

function detailText(d: Record<string, unknown> | null): string {
  if (!d || Object.keys(d).length === 0) return "";
  try {
    return JSON.stringify(d);
  } catch {
    return "";
  }
}

export function AuditPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["audit"],
    queryFn: () =>
      api.get<{ audit: AuditRow[] | null }>("/api/admin/audit?limit=200"),
  });

  const rows = data?.audit ?? [];

  return (
    <div>
      <PageHeader
        title="审计"
        desc="所有敏感操作的不可变审计记录（最近 200 条）。"
      />

      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}

      {data && (
        <Card className="overflow-hidden">
          <div className="scroll-thin overflow-x-auto">
            <table className="w-full min-w-[880px] text-sm">
              <thead>
                <tr className="border-b border-slate-200 bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-400">
                  <th className="px-4 py-2.5 font-medium">时间</th>
                  <th className="px-4 py-2.5 font-medium">操作人</th>
                  <th className="px-4 py-2.5 font-medium">动作</th>
                  <th className="px-4 py-2.5 font-medium">对象类型</th>
                  <th className="px-4 py-2.5 font-medium">对象 ID</th>
                  <th className="px-4 py-2.5 font-medium">详情</th>
                  <th className="px-4 py-2.5 font-medium">IP</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const detail = detailText(r.Detail);
                  return (
                    <tr
                      key={r.ID}
                      className="border-b border-slate-100 last:border-0 hover:bg-slate-50"
                    >
                      <td className="whitespace-nowrap px-4 py-2.5 text-slate-600">
                        {fmtDateTime(r.At)}
                      </td>
                      <td className="px-4 py-2.5 text-slate-700">
                        {r.ActorName || (
                          <span className="text-slate-400">系统</span>
                        )}
                      </td>
                      <td className="px-4 py-2.5">
                        <span className="rounded bg-slate-100 px-2 py-0.5 font-mono text-xs text-slate-700">
                          {r.Action}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 text-slate-600">
                        {r.TargetType || "-"}
                      </td>
                      <td className="px-4 py-2.5 font-mono text-xs text-slate-600">
                        {r.TargetID || "-"}
                      </td>
                      <td className="max-w-[240px] px-4 py-2.5">
                        {detail ? (
                          <span
                            title={detail}
                            className="block truncate font-mono text-xs text-slate-500"
                          >
                            {detail}
                          </span>
                        ) : (
                          <span className="text-slate-300">-</span>
                        )}
                      </td>
                      <td className="px-4 py-2.5 font-mono text-xs text-slate-500">
                        {r.IP || "-"}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {rows.length === 0 && <Empty label="暂无审计记录" />}
        </Card>
      )}
    </div>
  );
}
