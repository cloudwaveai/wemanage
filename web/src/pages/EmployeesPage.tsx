import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { EmployeeRow } from "../lib/types";
import {
  Card,
  PageHeader,
  Loading,
  ErrorBox,
  Empty,
  StatusBadge,
} from "../components/ui";

export function EmployeesPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["admin-employees"],
    queryFn: () =>
      api.get<{ employees: EmployeeRow[] }>("/api/admin/employees"),
  });

  const rows = data?.employees ?? [];

  return (
    <div>
      <PageHeader title="员工" desc="已同步的飞书员工及其绑定情况。" />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && (
        <Card className="overflow-hidden">
          <div className="scroll-thin overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead>
                <tr className="border-b border-slate-200 bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-400">
                  <th className="px-4 py-2.5 font-medium">姓名</th>
                  <th className="px-4 py-2.5 font-medium">邮箱</th>
                  <th className="px-4 py-2.5 font-medium">状态</th>
                  <th className="px-4 py-2.5 font-medium">绑定账号</th>
                  <th className="px-4 py-2.5 font-medium">Key 前缀</th>
                  <th className="px-4 py-2.5 font-medium">部门</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((e) => (
                  <tr
                    key={e.id}
                    className="border-b border-slate-100 last:border-0 hover:bg-slate-50"
                  >
                    <td className="px-4 py-2.5">
                      <div className="font-medium text-slate-800">
                        <Link
                          to={`/risk/employees/${e.id}`}
                          className="hover:text-indigo-600 hover:underline"
                          title="查看设备与风险"
                        >
                          {e.name}
                        </Link>
                        {e.is_super_admin && (
                          <span className="ml-1.5 rounded bg-indigo-50 px-1.5 py-0.5 text-[10px] font-medium text-indigo-600">
                            管理员
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-2.5 text-slate-600">
                      {e.email || "-"}
                    </td>
                    <td className="px-4 py-2.5">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <StatusBadge
                          status={e.status === "active" ? "active" : "disabled"}
                          fallbackLabel={e.status}
                        />
                        {e.frozen && (
                          <span
                            className="inline-flex items-center rounded-full bg-rose-100 px-2 py-0.5 text-xs font-medium text-rose-700"
                            title={e.frozen_reason || undefined}
                          >
                            已冻结
                          </span>
                        )}
                        {e.open_risk_events > 0 && (
                          <Link
                            to={`/risk/employees/${e.id}`}
                            className="inline-flex items-center rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 hover:bg-amber-200"
                          >
                            {e.open_risk_events} 条风险
                          </Link>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-2.5 text-slate-600">
                      {e.account_email ? (
                        <div className="flex items-center gap-2">
                          <span>{e.account_email}</span>
                          {e.account_status && (
                            <StatusBadge status={e.account_status} />
                          )}
                        </div>
                      ) : (
                        <span className="text-slate-400">未绑定</span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs text-slate-600">
                      {e.active_key_prefix ? (
                        <>
                          {e.active_key_prefix}
                          <span className="text-slate-300">…</span>
                        </>
                      ) : (
                        <span className="font-sans text-slate-400">无</span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-slate-600">
                      {e.department_names?.length
                        ? e.department_names.join("、")
                        : "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {rows.length === 0 && <Empty label="暂无员工，请先同步飞书" />}
        </Card>
      )}
    </div>
  );
}
