import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { AccountRow, EmployeeRow, IPGroupRow } from "../lib/types";
import {
  Card,
  PageHeader,
  Loading,
  ErrorBox,
  Empty,
  Button,
  StatusBadge,
} from "../components/ui";
import { fmtCompact, fmtDateTime, fmtNum } from "../lib/format";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { AddAccountWizard } from "../components/AddAccountWizard";

// accountName is the account's email, else a name derived from its provider
// (Kimi Code logins carry no email; admins can set one via 编辑).
function accountName(a: AccountRow): string {
  if (a.email) return a.email;
  return `${a.provider === "kimi" ? "Kimi Code" : "ChatGPT"} #${a.id}`;
}

// QuotaRow is one compact quota window: label, bar, percent; times on hover.
function QuotaRow({
  label,
  pct,
  resetAt,
  at,
}: {
  label: string;
  pct: number | null | undefined;
  resetAt: string | null | undefined;
  at: string | undefined;
}) {
  const stale = !!resetAt && new Date(resetAt).getTime() < Date.now();
  const v = pct ?? 0;
  const color = v >= 90 ? "bg-rose-500" : v >= 70 ? "bg-amber-500" : "bg-indigo-500";
  const title = [
    resetAt ? `重置于 ${fmtDateTime(resetAt)}` : "",
    at ? `更新于 ${fmtDateTime(at)}` : "",
    stale ? "已过重置时间，等待下次更新" : "",
  ]
    .filter(Boolean)
    .join("\n");
  return (
    <div className={"flex items-center gap-2 text-xs" + (stale ? " opacity-50" : "")} title={title || undefined}>
      <span className="w-5 shrink-0 text-slate-400">{label}</span>
      <div className="h-1.5 w-24 overflow-hidden rounded-full bg-slate-100">
        {pct != null && (
          <div className={`h-full rounded-full ${color}`} style={{ width: `${Math.min(Math.max(v, 0), 100)}%` }} />
        )}
      </div>
      <span className="w-11 shrink-0 text-right tabular-nums text-slate-600">
        {pct == null ? "—" : `${v.toFixed(1)}%`}
      </span>
    </div>
  );
}

// ChatGPT and Kimi Code expose 5-hour / weekly quota; other providers do not.
function hasQuota(a: AccountRow): boolean {
  return !a.provider || a.provider === "codex" || a.provider === "kimi";
}

export function AccountsPage() {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [wizardOpen, setWizardOpen] = useState(false);
  const [bindFor, setBindFor] = useState<AccountRow | null>(null);
  const [editFor, setEditFor] = useState<AccountRow | null>(null);

  const accounts = useQuery({
    queryKey: ["accounts"],
    queryFn: () => api.get<{ accounts: AccountRow[] }>("/api/admin/accounts"),
  });

  const refetch = () => qc.invalidateQueries({ queryKey: ["accounts"] });

  const unbind = useMutation({
    mutationFn: (id: number) => api.post(`/api/admin/accounts/${id}/unbind`),
    onSuccess: () => {
      toast("已解绑", "success");
      refetch();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "解绑失败", "error"),
  });

  const refreshQuota = useMutation({
    mutationFn: (id: number) =>
      api.post(`/api/admin/accounts/${id}/quota/refresh`),
    onSuccess: () => {
      toast("额度已刷新", "success");
      refetch();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "刷新失败", "error"),
  });

  const rows = accounts.data?.accounts ?? [];

  return (
    <div>
      <PageHeader
        title="账号"
        desc="上游账号的绑定、额度与用量。5 小时 / 每周额度来自 ChatGPT / Kimi 官方接口：有请求时随请求更新，闲置时网关每 30 分钟查询一次；Token 为经过网关的用量。"
        right={
          <Button variant="primary" onClick={() => setWizardOpen(true)}>
            添加账号
          </Button>
        }
      />

      {accounts.isLoading && <Loading />}
      {accounts.error && <ErrorBox error={accounts.error} />}

      {accounts.data && (
        <Card className="overflow-hidden">
          <div className="scroll-thin overflow-x-auto">
            <table className="w-full min-w-[1080px] text-sm">
              <thead>
                <tr className="border-b border-slate-200 bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-400">
                  <th className="px-4 py-2.5 font-medium">账号</th>
                  <th className="px-4 py-2.5 font-medium">绑定员工</th>
                  <th className="px-4 py-2.5 font-medium">IP 组</th>
                  <th className="px-4 py-2.5 font-medium">状态</th>
                  <th className="px-4 py-2.5 font-medium">额度（5 小时 / 每周）</th>
                  <th className="px-4 py-2.5 text-right font-medium">
                    Token（24h / 7 天 / 累计）
                  </th>
                  <th className="px-4 py-2.5 text-right font-medium">并发</th>
                  <th className="px-4 py-2.5 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((a) => (
                  <tr
                    key={a.id}
                    className="border-b border-slate-100 last:border-0 hover:bg-slate-50"
                  >
                    <td className="max-w-[240px] px-4 py-2.5">
                      <div className="truncate font-medium text-slate-800" title={a.email || undefined}>
                        {accountName(a)}
                      </div>
                      <div className="mt-0.5 flex items-center gap-1.5 text-xs text-slate-400">
                        <span
                          className={
                            "rounded px-1.5 py-0.5 text-[10px] font-medium " +
                            (a.provider === "kimi"
                              ? "bg-violet-100 text-violet-700"
                              : "bg-emerald-50 text-emerald-700")
                          }
                        >
                          {a.provider === "kimi" ? "Kimi Code" : "ChatGPT"}
                        </span>
                        {a.plan_type && <span>{a.plan_type}</span>}
                        <span>{a.owner_type === "employee" ? "员工自有" : "公司账号"}</span>
                      </div>
                    </td>
                    <td className="px-4 py-2.5 text-slate-600">
                      {a.employee_name || (
                        <span className="text-slate-400">未绑定</span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-slate-600">
                      {a.ip_group_name || "-"}
                    </td>
                    <td className="px-4 py-2.5">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <StatusBadge status={a.status} />
                        {a.needs_login && (
                          <span className="text-[11px] text-rose-500">
                            需登录
                          </span>
                        )}
                        {a.cpa_drift && (
                          <span
                            title={`网关与 CPA 的配置不一致：${a.cpa_drift}`}
                            className="rounded bg-amber-50 px-1.5 py-0.5 text-[11px] text-amber-700"
                          >
                            配置漂移
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-2.5">
                      {hasQuota(a) ? (
                        <div className="space-y-1.5">
                          <QuotaRow
                            label="5h"
                            pct={a.quota?.primary_used_pct}
                            resetAt={a.quota?.primary_reset_at}
                            at={a.quota?.at}
                          />
                          <QuotaRow
                            label="周"
                            pct={a.quota?.secondary_used_pct}
                            resetAt={a.quota?.secondary_reset_at}
                            at={a.quota?.at}
                          />
                        </div>
                      ) : (
                        <span className="text-xs text-slate-400">无额度窗口</span>
                      )}
                    </td>
                    <td
                      className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-slate-600"
                      title={[
                        `请求：24h ${fmtNum(a.usage.requests_24h)} / 7 天 ${fmtNum(a.usage.requests_7d)} / 累计 ${fmtNum(a.usage.requests_all)}`,
                        a.usage.last_active
                          ? `最近使用 ${fmtDateTime(a.usage.last_active)}`
                          : "还没有经过网关的请求",
                      ].join("\n")}
                    >
                      {fmtCompact(a.usage.tokens_24h)}
                      <span className="text-slate-300"> / </span>
                      {fmtCompact(a.usage.tokens_7d)}
                      <span className="text-slate-300"> / </span>
                      {fmtCompact(a.usage.tokens_all)}
                    </td>
                    <td className="px-4 py-2.5 text-right tabular-nums text-slate-600">
                      {a.max_concurrency ?? (
                        <span className="text-slate-400">默认</span>
                      )}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5 text-right">
                      <div className="flex justify-end gap-1">
                        {hasQuota(a) && (
                          <button
                            onClick={() => refreshQuota.mutate(a.id)}
                            disabled={refreshQuota.isPending}
                            className="rounded px-2 py-1 text-xs font-medium text-slate-500 hover:bg-slate-100 disabled:opacity-50"
                            title="向上游查询一次当前的 5 小时 / 每周额度"
                          >
                            刷新额度
                          </button>
                        )}
                        {a.employee_id ? (
                          <button
                            onClick={() => unbind.mutate(a.id)}
                            className="rounded px-2 py-1 text-xs font-medium text-slate-500 hover:bg-slate-100"
                          >
                            解绑
                          </button>
                        ) : (
                          <button
                            onClick={() => setBindFor(a)}
                            className="rounded px-2 py-1 text-xs font-medium text-indigo-600 hover:bg-indigo-50"
                          >
                            绑定
                          </button>
                        )}
                        <button
                          onClick={() => setEditFor(a)}
                          className="rounded px-2 py-1 text-xs font-medium text-slate-500 hover:bg-slate-100"
                        >
                          编辑
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {rows.length === 0 && <Empty label="暂无账号" />}
        </Card>
      )}

      <AddAccountWizard
        open={wizardOpen}
        onClose={() => setWizardOpen(false)}
        onDone={refetch}
      />
      <BindModal
        account={bindFor}
        onClose={() => setBindFor(null)}
        onDone={refetch}
      />
      <EditModal
        account={editFor}
        onClose={() => setEditFor(null)}
        onDone={refetch}
      />
    </div>
  );
}

// ---------------------------------------------------------------- bind modal
function BindModal({
  account,
  onClose,
  onDone,
}: {
  account: AccountRow | null;
  onClose: () => void;
  onDone: () => void;
}) {
  const { toast } = useToast();
  const [empId, setEmpId] = useState("");

  const employees = useQuery({
    queryKey: ["admin-employees"],
    queryFn: () =>
      api.get<{ employees: EmployeeRow[] }>("/api/admin/employees"),
    enabled: !!account,
  });

  const bind = useMutation({
    mutationFn: () =>
      api.post(`/api/admin/accounts/${account!.id}/bind`, {
        employee_id: Number(empId),
      }),
    onSuccess: () => {
      toast("绑定成功", "success");
      onDone();
      onClose();
      setEmpId("");
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "绑定失败", "error"),
  });

  return (
    <Modal
      open={!!account}
      onClose={onClose}
      title={`绑定账号 ${account ? accountName(account) : ""}`}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button
            variant="primary"
            onClick={() => bind.mutate()}
            disabled={!empId || bind.isPending}
          >
            {bind.isPending ? "处理中…" : "确认绑定"}
          </Button>
        </>
      }
    >
      <label className="mb-1 block text-xs font-medium text-slate-500">
        选择员工
      </label>
      <select
        value={empId}
        onChange={(e) => setEmpId(e.target.value)}
        className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100"
      >
        <option value="">请选择员工</option>
        {(employees.data?.employees ?? [])
          .filter((e) => !e.account_id)
          .map((e) => (
            <option key={e.id} value={e.id}>
              {e.name}（{e.email || e.id}）
            </option>
          ))}
      </select>
    </Modal>
  );
}

// ---------------------------------------------------------------- edit modal
function EditModal({
  account,
  onClose,
  onDone,
}: {
  account: AccountRow | null;
  onClose: () => void;
  onDone: () => void;
}) {
  const { toast } = useToast();
  const [maxc, setMaxc] = useState("");
  const [status, setStatus] = useState("");
  const [ipGroup, setIpGroup] = useState("");
  const [email, setEmail] = useState("");

  const groups = useQuery({
    queryKey: ["ip-groups"],
    queryFn: () => api.get<{ ip_groups: IPGroupRow[] }>("/api/admin/ip-groups"),
    enabled: !!account,
  });

  // Sync form to the selected account when the modal target changes.
  const key = account?.id ?? -1;
  const [syncedKey, setSyncedKey] = useState(-1);
  if (account && syncedKey !== key) {
    setSyncedKey(key);
    setMaxc(account.max_concurrency != null ? String(account.max_concurrency) : "");
    setStatus(account.status);
    setIpGroup(String(account.ip_group_id));
    setEmail(account.email);
  }

  const save = useMutation({
    mutationFn: () => {
      // Only send fields that changed. Body uses the Go struct field names
      // (AccountPatch has no json tags), which is what the backend parses.
      const patch: Record<string, unknown> = {};
      if (account) {
        if (status && status !== account.status) patch.Status = status;
        const origMax =
          account.max_concurrency != null
            ? String(account.max_concurrency)
            : "";
        if (maxc !== origMax) {
          patch.MaxConcurrency = maxc.trim() === "" ? -1 : Number(maxc);
        }
        if (Number(ipGroup) !== account.ip_group_id) {
          patch.IPGroupID = Number(ipGroup);
        }
        if (email.trim() !== account.email) patch.Email = email.trim();
      }
      return api.patch(`/api/admin/accounts/${account!.id}`, patch);
    },
    onSuccess: () => {
      toast("已保存", "success");
      onDone();
      onClose();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "保存失败", "error"),
  });

  const inputCls =
    "w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";
  const ipChanged = account && Number(ipGroup) !== account.ip_group_id;

  return (
    <Modal
      open={!!account}
      onClose={onClose}
      title={`编辑账号 ${account ? accountName(account) : ""}`}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button
            variant="primary"
            onClick={() => save.mutate()}
            disabled={save.isPending}
          >
            {save.isPending ? "保存中…" : "保存"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div>
          <label className="mb-1 block text-xs font-medium text-slate-500">
            名称 / 邮箱
          </label>
          <input
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder={account?.provider === "kimi" ? "如 kimi-研发1 或登录用的手机号/邮箱" : "ChatGPT 登录邮箱"}
            className={inputCls}
          />
          <p className="mt-1 text-xs text-slate-400">
            只用于在后台辨认账号。ChatGPT 账号登录时会自动填入邮箱；Kimi 登录不提供邮箱，可以在这里起个名字。
          </p>
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-slate-500">
            最大并发（留空表示使用默认值）
          </label>
          <input
            type="number"
            min={1}
            value={maxc}
            onChange={(e) => setMaxc(e.target.value)}
            placeholder="默认"
            className={inputCls}
          />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-slate-500">
            状态
          </label>
          <select
            value={status}
            onChange={(e) => setStatus(e.target.value)}
            className={inputCls}
          >
            <option value="active">启用</option>
            <option value="disabled">停用</option>
          </select>
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-slate-500">
            IP 组
          </label>
          <select
            value={ipGroup}
            onChange={(e) => setIpGroup(e.target.value)}
            className={inputCls}
          >
            {(groups.data?.ip_groups ?? []).map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
          </select>
          {ipChanged && (
            <p className="mt-1.5 text-xs text-amber-600">
              更改出口 IP 本身就是一个风险信号，请谨慎操作。
            </p>
          )}
        </div>
      </div>
    </Modal>
  );
}
