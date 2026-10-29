import { useState } from "react";
import { useMutation, useQueries, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { AccountRow, AccountResetCredits } from "../lib/types";
import { accountName, isChatGPT } from "../lib/providers";
import { fmtDateTime } from "../lib/format";
import { Button, Card, StatusBadge } from "./ui";
import { Modal } from "./Modal";
import { useToast } from "./Toast";

type Credits = Pick<AccountResetCredits, "account_id" | "available" | "credits">;
type Action = { account: AccountRow; type: "spend" | "reconcile" };

// Load each account independently so one unavailable provider cannot hide others.
export function AdminQuotaControl({ accounts }: { accounts: AccountRow[] }) {
  const [open, setOpen] = useState(false);
  const [action, setAction] = useState<Action | null>(null);
  const qc = useQueryClient();
  const { toast } = useToast();
  const codex = accounts.filter((a) => isChatGPT(a.provider));
  const creditQueries = useQueries({ queries: codex.map((a) => ({
    queryKey: ["admin-reset-credits", a.id],
    queryFn: () => api.get<Credits>(`/api/admin/accounts/${a.id}/reset-credits`),
    enabled: open,
    retry: false,
    staleTime: 60_000,
  })) });
  const credits = new Map(codex.map((a, i) => [a.id, creditQueries[i]]));
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["accounts"] });
    qc.invalidateQueries({ queryKey: ["admin-reset-credits"] });
    qc.invalidateQueries({ queryKey: ["my-quota"] });
    qc.invalidateQueries({ queryKey: ["my-reset-credits"] });
  };
  const perform = useMutation({
    mutationFn: (item: Action) => api.post<{ ok: boolean; message: string }>(
      `/api/admin/accounts/${item.account.id}/${item.type === "spend" ? "reset-credits/use" : "rate-limit/reconcile"}`,
    ),
    onSuccess: (result) => { toast(result.message, result.ok ? "success" : "info"); setAction(null); invalidate(); },
    onError: (err: unknown) => { toast(err instanceof Error ? err.message : "操作失败", "error"); setAction(null); invalidate(); },
  });
  const limited = accounts.filter((a) => a.status === "rate_limited").length;
  return (
    <Card className="mb-5 overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-3 p-5">
        <div>
          <h2 className="font-semibold text-slate-900">重置卡与限流管理</h2>
          <p className="mt-1 text-sm text-slate-500">查看所有账号的重置卡和限流状态。当前 {limited} 个账号限流。</p>
        </div>
        <Button onClick={() => setOpen(!open)}>{open ? "收起" : "查看和管理"}</Button>
      </div>
      {open && <div className="border-t border-slate-100 p-5">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs text-slate-500">
          <span>重置卡属于各自的账号；查询失败不会显示为 0 张。</span>
          <button className="font-medium text-indigo-600 hover:underline" onClick={() => qc.invalidateQueries({ queryKey: ["admin-reset-credits"] })}>重新查询重置卡</button>
        </div>
        <div className="space-y-3">
          {accounts.length === 0 && <p className="py-4 text-sm text-slate-400">暂无账号。</p>}
          {accounts.map((a) => {
            const query = credits.get(a.id);
            const list = [...(query?.data?.credits ?? [])].sort((x, y) => (x.expires_at ?? "9999").localeCompare(y.expires_at ?? "9999"));
            return <div key={a.id} className="rounded-xl border border-slate-200 p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2"><span className="font-medium text-slate-900">{accountName(a)}</span><StatusBadge status={a.status} /></div>
                  <div className="mt-1 text-xs text-slate-500">使用人：{a.members.map((m) => m.name).join("、") || "未分配"}{a.status === "rate_limited" && a.rate_limited_until ? ` · 限流至 ${fmtDateTime(a.rate_limited_until)}` : ""}</div>
                  <div className="mt-2 text-sm text-slate-600">
                    {isChatGPT(a.provider) ? query?.isLoading ? "正在查询重置卡…" : query?.error ? <span className="text-amber-700">查询失败：{query.error instanceof Error ? query.error.message : "请重试"}</span> : `可用重置卡 ${query?.data?.available ?? "—"} 张` : "此账号不支持 ChatGPT 重置卡"}
                  </div>
                  {list.length > 0 && <div className="mt-1 space-y-1 text-xs text-slate-500">{list.map((c) => <div key={c.id}>{c.title || "重置卡"} · {c.expires_at ? `${fmtDateTime(c.expires_at)} 到期` : "无到期时间"}</div>)}</div>}
                </div>
                {isChatGPT(a.provider) && <div className="flex flex-wrap gap-2">
                  <Button disabled={perform.isPending} onClick={() => setAction({ account: a, type: "reconcile" })}>核对并恢复限流</Button>
                  <Button variant="primary" disabled={perform.isPending || !query?.data?.available || list.length === 0} onClick={() => setAction({ account: a, type: "spend" })}>使用重置卡</Button>
                </div>}
              </div>
            </div>;
          })}
        </div>
      </div>}
      <Modal open={!!action} onClose={perform.isPending ? undefined : () => setAction(null)} title={action?.type === "spend" ? "确认使用重置卡" : "核对并恢复限流"} footer={<>
        <Button disabled={perform.isPending} onClick={() => setAction(null)}>取消</Button>
        <Button variant="primary" disabled={perform.isPending} onClick={() => action && perform.mutate(action)}>{perform.isPending ? "处理中…" : "确认"}</Button>
      </>}>
        {action?.type === "spend" ? <p className="text-sm leading-6 text-slate-600">将为账号「{accountName(action.account)}」使用一张重置卡，优先使用最早到期的卡。使用后该账号的额度窗口会重新计算，重置卡会被消耗。</p> : <p className="text-sm leading-6 text-slate-600">系统会先向上游核对「{action ? accountName(action.account) : ""}」的当前额度。只有上游明确表示额度可用时，才清除该账号的旧限流状态；此操作不使用重置卡。</p>}
      </Modal>
    </Card>
  );
}
