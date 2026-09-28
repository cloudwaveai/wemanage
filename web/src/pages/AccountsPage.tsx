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
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { AddAccountWizard } from "../components/AddAccountWizard";

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

  const rows = accounts.data?.accounts ?? [];

  return (
    <div>
      <PageHeader
        title="账号"
        desc="上游 ChatGPT 账号及其绑定与并发配置。"
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
            <table className="w-full min-w-[960px] text-sm">
              <thead>
                <tr className="border-b border-slate-200 bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-400">
                  <th className="px-4 py-2.5 font-medium">邮箱</th>
                  <th className="px-4 py-2.5 font-medium">套餐</th>
                  <th className="px-4 py-2.5 font-medium">归属</th>
                  <th className="px-4 py-2.5 font-medium">绑定员工</th>
                  <th className="px-4 py-2.5 font-medium">IP 组</th>
                  <th className="px-4 py-2.5 font-medium">状态</th>
                  <th className="px-4 py-2.5 text-right font-medium">并发</th>
                  <th className="px-4 py-2.5 font-medium">漂移</th>
                  <th className="px-4 py-2.5 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((a) => (
                  <tr
                    key={a.id}
                    className="border-b border-slate-100 last:border-0 hover:bg-slate-50"
                  >
                    <td className="px-4 py-2.5 font-medium text-slate-800">
                      {a.email || <span className="text-slate-400">—</span>}
                    </td>
                    <td className="px-4 py-2.5 text-slate-600">
                      {a.plan_type || "-"}
                      {a.provider && a.provider !== "codex" && (
                        <span className="ml-1.5 rounded bg-violet-100 px-1.5 py-0.5 text-[10px] font-medium text-violet-700">
                          {a.provider === "kimi" ? "Kimi" : a.provider}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-slate-600">
                      {a.owner_type || "-"}
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
                      <div className="flex items-center gap-1.5">
                        <StatusBadge status={a.status} />
                        {a.needs_login && (
                          <span className="text-[11px] text-rose-500">
                            需登录
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-2.5 text-right tabular-nums text-slate-600">
                      {a.max_concurrency ?? (
                        <span className="text-slate-400">默认</span>
                      )}
                    </td>
                    <td className="max-w-[140px] px-4 py-2.5">
                      {a.cpa_drift ? (
                        <span
                          title={a.cpa_drift}
                          className="inline-block max-w-full truncate rounded bg-amber-50 px-1.5 py-0.5 text-[11px] text-amber-700"
                        >
                          {a.cpa_drift}
                        </span>
                      ) : (
                        <span className="text-xs text-slate-300">正常</span>
                      )}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5 text-right">
                      <div className="flex justify-end gap-1">
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
      title={`绑定账号 ${account?.email ?? ""}`}
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
      title={`编辑账号 ${account?.email ?? ""}`}
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
