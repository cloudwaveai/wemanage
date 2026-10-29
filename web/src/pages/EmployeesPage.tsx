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
import { CodeBlock } from "../components/CopyButton";
import { useToast } from "../components/Toast";
import { useAuth } from "../context/AuthContext";
import { accountName } from "../lib/providers";
import { fmtDateTime } from "../lib/format";
import type { APIChannel } from "./ChannelsPage";
import type { Project } from "./ProjectsPage";

type ServiceKey = { id: number; label: string; prefix: string; channel_id: number | null; project_id: number | null; created_at: string; last_used_at: string | null };


export function EmployeesPage() {
  const { me } = useAuth();
  const { toast } = useToast();
  const qc = useQueryClient();
  const [adminFor, setAdminFor] = useState<EmployeeRow | null>(null);
  const [creating, setCreating] = useState(false);
  const [svcName, setSvcName] = useState("");
  const [keyFor, setKeyFor] = useState<EmployeeRow | null>(null);
  const [keyLabel, setKeyLabel] = useState("");
  const [keyChannel, setKeyChannel] = useState("");
  const [keyProject, setKeyProject] = useState("");
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.get<{ projects: Project[] }>("/api/admin/projects") });
  const channels = useQuery({ queryKey: ["api-channels"], queryFn: () => api.get<{ channels: APIChannel[] }>("/api/admin/api-channels") });
  const [issued, setIssued] = useState<{ name: string; key: string } | null>(null);
  const { data, isLoading, error } = useQuery({
    queryKey: ["admin-employees"],
    queryFn: () =>
      api.get<{ employees: EmployeeRow[] }>("/api/admin/employees"),
  });

  const serviceKeys = useQuery({
    queryKey: ["service-keys", keyFor?.id],
    queryFn: () => api.get<{ keys: ServiceKey[] }>(`/api/admin/employees/${keyFor!.id}/keys`),
    enabled: !!keyFor,
  });
  const revokeKey = useMutation({
    mutationFn: (id: number) => api.del(`/api/admin/employees/${keyFor!.id}/keys/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["service-keys", keyFor?.id] }); qc.invalidateQueries({ queryKey: ["admin-employees"] }); toast("已撤销；网关缓存更新后生效", "success"); },
    onError: (err: unknown) => toast(err instanceof Error ? err.message : "撤销失败", "error"),
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

  const createService = useMutation({
    mutationFn: async (name: string) => {
      const acc = await api.post<{ id: number; name: string }>("/api/admin/service-accounts", { name });
      const k = await api.post<{ key: string }>(`/api/admin/employees/${acc.id}/key`, {});
      return { name: acc.name, key: k.key };
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["admin-employees"] });
      setCreating(false);
      setSvcName("");
      setIssued(r);
    },
    onError: (err: unknown) =>
      toast(err instanceof Error ? err.message : "创建失败", "error"),
  });

  const issueKey = useMutation({
    mutationFn: (e: EmployeeRow) =>
      api.post<{ key: string }>(`/api/admin/employees/${e.id}/key`, { label: keyLabel.trim(), channel_id: keyChannel ? Number(keyChannel) : null, project_id: keyProject ? Number(keyProject) : null }),
    onSuccess: (r, e) => {
      qc.invalidateQueries({ queryKey: ["admin-employees"] });
      qc.invalidateQueries({ queryKey: ["service-keys", e.id] });
      setKeyLabel("");
      setKeyChannel("");
      setKeyProject("");
      setIssued({ name: e.name, key: r.key });
    },
    onError: (err: unknown) =>
      toast(err instanceof Error ? err.message : "生成失败", "error"),
  });

  const rows = data?.employees ?? [];

  return (
    <div>
      <PageHeader
        title="员工"
        desc="已同步的飞书员工及其绑定情况。服务账号给公司自己的平台（如 New API）调用，不在飞书里。"
        right={<Button onClick={() => setCreating(true)}>新建服务账号</Button>}
      />
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
                        {e.is_service && (
                          <span className="ml-1.5 rounded bg-emerald-50 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700">
                            服务账号
                          </span>
                        )}
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
                              <span>{accountName(a)}</span>
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
                      {e.is_service && (
                        <Button
                          variant="ghost"
                          className="ml-1 !px-2 !py-1 font-sans text-xs"
                          onClick={() => setKeyFor(e)}
                        >
                          管理密钥
                        </Button>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-slate-600">
                      {e.department_names?.length
                        ? e.department_names.join("、")
                        : "-"}
                    </td>
                    <td className="px-4 py-2.5">
                      {e.is_service ? (
                        <span className="text-xs text-slate-400">-</span>
                      ) : e.id === me?.id ? (
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

      <Modal
        open={creating}
        onClose={() => setCreating(false)}
        title="新建服务账号"
        footer={
          <>
            <Button onClick={() => setCreating(false)}>取消</Button>
            <Button
              variant="primary"
              disabled={!svcName.trim() || createService.isPending}
              onClick={() => createService.mutate(svcName.trim())}
            >
              {createService.isPending ? "创建中…" : "创建并生成 Key"}
            </Button>
          </>
        }
      >
        <div className="space-y-3 text-sm text-slate-600">
          <p>
            服务账号给公司自己的平台调用网关（例如 New API 把 gpt-6-luna 接成一个渠道），不对应飞书里的人：
            不用确认设备、不受公司网络限制，没有周报和额度提醒。
          </p>
          <input
            value={svcName}
            onChange={(e) => setSvcName(e.target.value)}
            maxLength={50}
            placeholder="名称，例如 New API 平台"
            className="w-full rounded-lg border border-slate-300 px-3 py-2 outline-none focus:border-indigo-400"
          />
          <p className="text-xs text-slate-400">
            创建后到「账号」页给它分配 ChatGPT 账号，并打开那个账号的"第三方客户端"（平台的请求不是 Codex 发的）。
          </p>
        </div>
      </Modal>

      <Modal
        open={!!keyFor}
        onClose={() => setKeyFor(null)}
        title={`管理 ${keyFor?.name ?? ""} 的应用密钥`}
        footer={<Button onClick={() => setKeyFor(null)}>关闭</Button>}
      >
        <p className="text-sm text-slate-600">每个应用使用独立密钥。新建不会替换其他密钥；撤销会在网关缓存刷新后生效，数据库断连时最长可能延迟 10 分钟。</p>
        {serviceKeys.isLoading && <Loading />}
        {serviceKeys.error && <ErrorBox error={serviceKeys.error} />}
        <div className="mt-3 space-y-2">
          {serviceKeys.data?.keys.map((key) => (
            <div key={key.id} className="flex items-center justify-between gap-3 rounded border border-slate-200 p-3 text-sm">
              <div><div>{key.label || "未命名"} {key.project_id ? `· ${projects.data?.projects.find((p) => p.id === key.project_id)?.name || "项目"}` : "· 未分配项目"} {key.channel_id ? `· ${channels.data?.channels.find((c) => c.id === key.channel_id)?.name || "渠道"}` : "· 订阅账号"} · <span className="font-mono">{key.prefix}…</span></div><div className="text-xs text-slate-500">创建于 {fmtDateTime(key.created_at)}{key.last_used_at && ` · 最近使用 ${fmtDateTime(key.last_used_at)}`}</div></div>
              <Button variant="danger" disabled={revokeKey.isPending} onClick={() => { if (window.confirm(`撤销 ${key.label || key.prefix}？使用该密钥的应用将无法继续调用。`)) revokeKey.mutate(key.id); }}>撤销</Button>
            </div>
          ))}
        </div>
        <label className="mt-4 block text-sm text-slate-700">密钥用途<select className="mt-1 w-full rounded-lg border border-slate-300 px-3 py-2" value={keyChannel} onChange={(e) => setKeyChannel(e.target.value)}><option value="">使用已绑定的订阅账号</option>{channels.data?.channels.filter((c) => c.active).map((c) => <option key={c.id} value={c.id}>{c.name}（{c.protocol}）</option>)}</select></label>
        <label className="mt-3 block text-sm text-slate-700">项目归属<select className="mt-1 w-full rounded-lg border border-slate-300 px-3 py-2" value={keyProject} onChange={(e) => setKeyProject(e.target.value)}><option value="">暂不分配</option>{projects.data?.projects.map((p) => <option key={p.id} value={p.id}>{p.name}（{p.category === "client" ? "客户" : "基础建设"}）</option>)}</select></label>
        <div className="mt-4 flex gap-2">
          <input value={keyLabel} onChange={(e) => setKeyLabel(e.target.value)} maxLength={50} placeholder="应用名称，例如内部知识库" className="min-w-0 flex-1 rounded-lg border border-slate-300 px-3 py-2 text-sm" />
          <Button variant="primary" disabled={issueKey.isPending} onClick={() => keyFor && issueKey.mutate(keyFor)}>{issueKey.isPending ? "生成中…" : "新增密钥"}</Button>
        </div>
        <p className="mt-2 text-xs text-slate-500">密钥只在生成时显示一次；操作会记录到审计。</p>
      </Modal>

      <Modal
        open={!!issued}
        onClose={() => setIssued(null)}
        title={`${issued?.name ?? ""} 的 Key`}
        footer={<Button onClick={() => setIssued(null)}>我已保存</Button>}
      >
        <div className="space-y-3 text-sm text-slate-600">
          <p>Key 只完整显示这一次，请复制到平台的渠道配置里（Base URL 填 {window.location.origin}/v1）。</p>
          {issued && <CodeBlock code={issued.key} />}
        </div>
      </Modal>
    </div>
  );
}
