import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { EmployeeRow } from "../lib/types";
import {
  Button,
  Card,
  PageHeader,
  Loading,
  ErrorBox,
  Empty,
  StatusBadge,
} from "../components/ui";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { useAuth } from "../context/AuthContext";

// accountLabel names a bound account: its email, else its provider and id
// (Kimi Code logins carry no email).
function accountLabel(a: EmployeeRow["accounts"][number]): string {
  if (a.email) return a.email;
  const kind = a.provider === "kimi" ? "Kimi Code" : "ChatGPT";
  return `${kind} #${a.id}`;
}

export function EmployeesPage() {
  const { me } = useAuth();
  const { toast } = useToast();
  const qc = useQueryClient();
  const [adminFor, setAdminFor] = useState<EmployeeRow | null>(null);
  const { data, isLoading, error } = useQuery({
    queryKey: ["admin-employees"],
    queryFn: () =>
      api.get<{ employees: EmployeeRow[] }>("/api/admin/employees"),
  });

  const setAdmin = useMutation({
    mutationFn: (e: EmployeeRow) =>
      api.post(`/api/admin/employees/${e.id}/super-admin`, {
        value: !e.is_super_admin,
      }),
    onSuccess: (_, e) => {
      toast(
        e.is_super_admin
          ? `已取消 ${e.name} 的超级管理员`
          : `已把 ${e.name} 设为超级管理员`,
        "success",
      );
      qc.invalidateQueries({ queryKey: ["admin-employees"] });
      setAdminFor(null);
    },
    onError: (err: unknown) =>
      toast(err instanceof Error ? err.message : "操作失败", "error"),
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
                  <th className="px-4 py-2.5 font-medium">超级管理员</th>
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
                        {e.remote_allowed && (
                          <span
                            className="inline-flex items-center rounded-full bg-sky-100 px-2 py-0.5 text-xs font-medium text-sky-700"
                            title="允许在公司网络外使用"
                          >
                            可远程
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
                      {e.accounts.length > 0 ? (
                        <div className="space-y-1">
                          {e.accounts.map((a) => (
                            <div key={a.id} className="flex items-center gap-2">
                              <span>{accountLabel(a)}</span>
                              <StatusBadge status={a.status} />
                            </div>
                          ))}
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
                    <td className="px-4 py-2.5">
                      {e.id === me?.id ? (
                        <span className="text-xs text-slate-400">本人</span>
                      ) : e.super_admin_pinned ? (
                        <span
                          className="text-xs text-slate-400"
                          title="由服务器配置 BOOTSTRAP_SUPER_ADMINS 指定，要取消请先从配置里去掉"
                        >
                          配置指定
                        </span>
                      ) : e.is_super_admin ? (
                        <Button
                          variant="ghost"
                          className="!px-2 !py-1 text-xs"
                          onClick={() => setAdminFor(e)}
                        >
                          取消
                        </Button>
                      ) : e.status === "active" ? (
                        <Button
                          variant="ghost"
                          className="!px-2 !py-1 text-xs"
                          onClick={() => setAdminFor(e)}
                        >
                          设为管理员
                        </Button>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {rows.length === 0 && <Empty label="暂无员工，请先同步飞书" />}
        </Card>
      )}
      <Modal
        open={!!adminFor}
        onClose={() => setAdminFor(null)}
        title={adminFor?.is_super_admin ? "取消超级管理员" : "设为超级管理员"}
        footer={
          <>
            <Button onClick={() => setAdminFor(null)}>取消</Button>
            <Button
              variant={adminFor?.is_super_admin ? "danger" : "primary"}
              disabled={setAdmin.isPending}
              onClick={() => adminFor && setAdmin.mutate(adminFor)}
            >
              {setAdmin.isPending ? "处理中…" : "确认"}
            </Button>
          </>
        }
      >
        <p className="text-sm text-slate-600">
          {adminFor?.is_super_admin
            ? `确认取消 ${adminFor?.name} 的超级管理员？取消后立即生效，他只能看到自己的数据。`
            : `确认把 ${adminFor?.name} 设为超级管理员？超级管理员可以查看所有员工的对话日志、管理账号和设置，也会收到账号告警。`}
        </p>
        <p className="mt-2 text-xs text-slate-400">此操作会记录到审计。</p>
      </Modal>
    </div>
  );
}
