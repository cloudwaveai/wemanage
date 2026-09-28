import { useState } from "react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { TeamMember, TeamUsageResp } from "../lib/types";
import {
  Card,
  PageHeader,
  StatTile,
  Loading,
  ErrorBox,
  Empty,
  StatusBadge,
  QuotaCell,
  RangeTabs,
} from "../components/ui";
import { TableCardHeader } from "../components/risk";
import { useAuth } from "../context/AuthContext";
import { fmtNum, fmtCompact, fmtDateTime } from "../lib/format";

const thCls = "px-3 py-2.5 font-medium";

function MemberRow({ m, linkable }: { m: TeamMember; linkable: boolean }) {
  const depts = m.department_names?.join("、") ?? "";
  const [day, time] = m.last_active ? fmtDateTime(m.last_active).split(" ") : [];
  return (
    <tr className="border-b border-slate-100 last:border-0 hover:bg-slate-50">
      <td className="max-w-[180px] px-4 py-2.5">
        <div className="flex items-center gap-1.5">
          {linkable ? (
            <Link
              to={`/risk/employees/${m.employee_id}`}
              className="truncate font-medium text-slate-800 hover:text-indigo-600 hover:underline"
              title="查看设备与风险"
            >
              {m.name}
            </Link>
          ) : (
            <span className="truncate font-medium text-slate-800">{m.name}</span>
          )}
          {m.frozen && (
            <span className="inline-flex shrink-0 items-center rounded-full bg-rose-100 px-2 py-0.5 text-xs font-medium text-rose-700">
              已冻结
            </span>
          )}
        </div>
        <div className="truncate text-[11px] text-slate-400" title={depts || undefined}>
          {depts || "—"}
        </div>
      </td>
      <td
        className={
          "whitespace-nowrap px-3 py-2.5 text-right tabular-nums " +
          (m.requests ? "text-slate-700" : "text-slate-300")
        }
      >
        {fmtNum(m.requests)}
      </td>
      <td
        className="whitespace-nowrap px-3 py-2.5 text-right tabular-nums"
        title={`输入 ${fmtNum(m.tokens_input)} · 输出 ${fmtNum(m.tokens_output)}`}
      >
        <div className="text-slate-700">{fmtCompact(m.tokens_input)}</div>
        <div className="text-[11px] text-slate-400">{fmtCompact(m.tokens_output)}</div>
      </td>
      <td className="whitespace-nowrap px-3 py-2.5 text-slate-600">
        {m.last_active ? (
          <div title={fmtDateTime(m.last_active)}>
            <div>{day}</div>
            <div className="text-[11px] text-slate-400">{time}</div>
          </div>
        ) : (
          <span className="text-slate-300">—</span>
        )}
      </td>
      <td className="whitespace-nowrap px-3 py-2.5">
        {!m.has_account ? (
          <span className="text-xs text-slate-400">未绑定</span>
        ) : m.account_status ? (
          <StatusBadge status={m.account_status} />
        ) : (
          <span className="text-slate-300">—</span>
        )}
      </td>
      <td className="px-3 py-2.5">
        <QuotaCell
          pct={m.quota?.primary_used_pct}
          resetAt={m.quota?.primary_reset_at}
          at={m.quota?.at}
        />
      </td>
      <td className="px-3 py-2.5 pr-4">
        <QuotaCell
          pct={m.quota?.secondary_used_pct}
          resetAt={m.quota?.secondary_reset_at}
          at={m.quota?.at}
        />
      </td>
    </tr>
  );
}

export function TeamPage() {
  const { me } = useAuth();
  const isAdmin = me?.role === "super_admin";
  const [range, setRange] = useState(7);

  const q = useQuery({
    queryKey: ["team-usage", range],
    queryFn: () => api.get<TeamUsageResp>(`/api/team/usage?range=${range}`),
  });

  const u = q.data?.summary;
  const members = q.data?.members ?? [];
  const totalTokens = u
    ? u.tokens_input + u.tokens_output + u.tokens_reasoning
    : 0;
  const activeCount = members.filter((m) => m.requests > 0).length;

  return (
    <div>
      <PageHeader
        title="团队用量"
        desc={
          isAdmin
            ? "全部员工的请求量、token 消耗与账号额度。"
            : "你负责部门成员的请求量、token 消耗与账号额度。"
        }
        right={<RangeTabs value={range} onChange={setRange} />}
      />

      {q.isLoading && <Loading />}
      {q.error && <ErrorBox error={q.error} />}

      {u && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <StatTile
              label="请求数"
              value={fmtNum(u.requests)}
              hint={`活跃成员 ${activeCount} / ${members.length}`}
            />
            <StatTile label="总 Token" value={fmtNum(totalTokens)} />
            <StatTile label="输入 Token" value={fmtNum(u.tokens_input)} />
            <StatTile label="输出 Token" value={fmtNum(u.tokens_output)} />
          </div>

          <Card className="overflow-hidden">
            <TableCardHeader
              title="成员"
              sub={`${members.length} 人 · 按请求数排序`}
            />
            <div className="scroll-thin overflow-x-auto">
              <table className="w-full min-w-[700px] text-sm">
                <thead>
                  <tr className="border-b border-slate-200 bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-400">
                    <th className="px-4 py-2.5 font-medium">姓名</th>
                    <th className={thCls + " text-right"}>请求数</th>
                    <th
                      className={thCls + " whitespace-nowrap text-right"}
                      title="上：输入 Token；下：输出 Token"
                    >
                      输入 / 输出
                    </th>
                    <th className={thCls}>最近使用</th>
                    <th className={thCls}>账号状态</th>
                    <th className={thCls}>5 小时额度</th>
                    <th className={thCls + " pr-4"}>每周额度</th>
                  </tr>
                </thead>
                <tbody>
                  {members.map((m) => (
                    <MemberRow key={m.employee_id} m={m} linkable={isAdmin} />
                  ))}
                </tbody>
              </table>
            </div>
            {members.length === 0 && <Empty label="暂无成员" />}
          </Card>
        </div>
      )}
    </div>
  );
}
