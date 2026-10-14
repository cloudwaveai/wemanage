import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, LIVE_MS } from "../lib/api";
import type { TeamMember, TeamUsageResp } from "../lib/types";
import {
  Card,
  PageHeader,
  StatTile,
  Loading,
  ErrorBox,
  Empty,
  StatusBadge,
  RangeTabs,
} from "../components/ui";
import { TableCardHeader } from "../components/risk";
import { useAuth } from "../context/AuthContext";
import { fmtNum, fmtDateTime } from "../lib/format";

const thCls = "px-3 py-2.5 font-medium";

// MemberRow shows only whether the member's account works, so a leader can
// help when it does not. No usage numbers or activity times per person.
function MemberRow({ m }: { m: TeamMember }) {
  const depts = m.department_names?.join("、") ?? "";
  return (
    <tr className="border-b border-slate-100 last:border-0 hover:bg-slate-50">
      <td className="max-w-[220px] px-4 py-2.5">
        <div className="truncate font-medium text-slate-800">{m.name}</div>
        <div className="truncate text-[11px] text-slate-400" title={depts || undefined}>
          {depts || "—"}
        </div>
      </td>
      <td className="whitespace-nowrap px-3 py-2.5">
        {!m.has_account ? (
          <span className="text-xs text-slate-400">还没开通</span>
        ) : m.account_status ? (
          <div className="flex items-center gap-2">
            <StatusBadge status={m.account_status} />
            {m.account_status === "rate_limited" && m.rate_limited_until && (
              <span className="text-xs text-slate-400">
                {fmtDateTime(m.rate_limited_until)} 恢复
              </span>
            )}
          </div>
        ) : (
          <span className="text-slate-300">—</span>
        )}
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
    refetchInterval: LIVE_MS, // new requests and quota land within about a minute
    refetchOnWindowFocus: true,
  });

  const u = q.data?.summary;
  const members = q.data?.members ?? [];
  const totalTokens = u
    ? u.tokens_input + u.tokens_output + u.tokens_reasoning
    : 0;

  return (
    <div>
      <PageHeader
        title="团队"
        desc={
          (isAdmin ? "全公司" : "你负责的部门") +
          "整体的 AI 使用量，以及每位成员的账号能不能正常用（额度用完或需要重新登录时，可以及时帮忙）。不展示个人用量和对话内容。"
        }
        right={<RangeTabs value={range} onChange={setRange} />}
      />

      {q.isLoading && <Loading />}
      {q.error && <ErrorBox error={q.error} />}

      {u && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <StatTile label="团队请求数" value={fmtNum(u.requests)} />
            <StatTile label="团队总 Token" value={fmtNum(totalTokens)} />
            <StatTile label="成员" value={fmtNum(members.length)} />
            <StatTile label="已开通账号" value={fmtNum(q.data?.with_account ?? 0)} />
          </div>

          <Card className="overflow-hidden">
            <TableCardHeader title="成员账号" sub={`${members.length} 人`} />
            <div className="scroll-thin overflow-x-auto">
              <table className="w-full min-w-[420px] text-sm">
                <thead>
                  <tr className="border-b border-slate-200 bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-400">
                    <th className="px-4 py-2.5 font-medium">成员</th>
                    <th className={thCls}>账号状态</th>
                  </tr>
                </thead>
                <tbody>
                  {members.map((m) => (
                    <MemberRow key={m.employee_id} m={m} />
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
