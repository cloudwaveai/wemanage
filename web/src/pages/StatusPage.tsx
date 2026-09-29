import React from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAuth } from "../context/AuthContext";
import type { MyStatus } from "../lib/types";
import { Card, PageHeader, Loading, ErrorBox, Meter, Switch, Button } from "../components/ui";
import { useToast } from "../components/Toast";
import { failureReason } from "../lib/failures";
import { fmtAgo, fmtNum, fmtWhen } from "../lib/format";

interface Issue {
  tone: "rose" | "amber" | "indigo";
  title: string;
  detail?: string;
  action?: React.ReactNode;
}

const TONES: Record<Issue["tone"], string> = {
  rose: "border-rose-200 bg-rose-50 text-rose-800",
  amber: "border-amber-200 bg-amber-50 text-amber-800",
  indigo: "border-indigo-200 bg-indigo-50 text-indigo-800",
};

const linkCls = "text-sm font-medium text-indigo-600 hover:underline";

// StatusPage answers "can I use it right now, and if not, what do I do": the
// employee's own view of their key, account, quota and devices.
export function StatusPage() {
  const { me, refetch } = useAuth();
  const { toast } = useToast();
  const qc = useQueryClient();
  const status = useQuery({
    queryKey: ["my-status"],
    queryFn: () => api.get<MyStatus>("/api/me/status"),
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["my-status"] });
    refetch();
  };
  const reminder = useMutation({
    mutationFn: (enabled: boolean) => api.post("/api/me/quota-alert", { enabled }),
    onSuccess: refresh,
    onError: (e: unknown) => toast(e instanceof Error ? e.message : "操作失败", "error"),
  });
  const confirm = useMutation({
    mutationFn: (id: number) => api.post(`/api/me/devices/${id}/confirm`),
    onSuccess: () => {
      toast("已确认", "success");
      refresh();
    },
    onError: (e: unknown) => toast(e instanceof Error ? e.message : "操作失败", "error"),
  });

  const s = status.data;
  const issues: Issue[] = [];
  if (s) {
    if (s.frozen) {
      issues.push({
        tone: "rose",
        title: "你的 Key 已被暂停" + (s.frozen_reason ? `（${s.frozen_reason}）` : ""),
        detail: "请联系管理员核实后解冻。",
      });
    }
    if (!s.has_key && !s.frozen) {
      issues.push({
        tone: "amber",
        title: "还没有 Key",
        action: <Link to="/key" className={linkCls}>去生成</Link>,
      });
    }
    if (!s.account.bound) {
      issues.push({
        tone: "amber",
        title: "还没有分配 AI 账号",
        detail: "已通知管理员，分配好后飞书会告诉你。",
      });
    } else if (s.account.status === "needs_login") {
      issues.push({
        tone: "amber",
        title: "AI 账号登录失效",
        detail: "管理员已收到通知，重新登录后就能继续用。",
      });
    } else if (s.account.status === "disabled") {
      issues.push({ tone: "rose", title: "AI 账号已被停用", detail: "请联系管理员。" });
    } else if (s.account.status === "rate_limited") {
      issues.push({
        tone: "amber",
        title: "账号额度用完了",
        detail: s.account.rate_limited_until
          ? `预计 ${fmtWhen(s.account.rate_limited_until)} 恢复。`
          : "稍后会自动恢复。",
      });
    }
    for (const d of s.pending_devices) {
      issues.push({
        tone: "amber",
        title: `新电脑「${d.label}」待确认`,
        detail: `${fmtAgo(d.first_seen)}开始使用${d.last_ip ? `，IP ${d.last_ip}` : ""}。是你本人的话点「是我」。`,
        action: (
          <div className="flex items-center gap-3">
            <Button
              variant="primary"
              onClick={() => confirm.mutate(d.id)}
              disabled={confirm.isPending}
            >
              是我
            </Button>
            <Link to="/devices" className="text-sm text-slate-500 hover:underline">
              不是我…
            </Link>
          </div>
        ),
      });
    }
    if (s.has_key && s.account.bound && !s.last_ok && !s.last_failure) {
      issues.push({
        tone: "indigo",
        title: "还没在 Codex 里用过",
        detail: "用一条命令配好 Codex，大约一分钟。",
        action: <Link to="/config" className={linkCls}>去一键配置</Link>,
      });
    }
    if (s.last_failure && issues.length === 0) {
      issues.push({
        tone: "amber",
        title: `最近一次请求失败：${failureReason(s.last_failure.status_code, s.last_failure.error_type) || s.last_failure.status_code}`,
        detail: `${fmtAgo(s.last_failure.at)}。之后还没有成功的请求。`,
        action: <Link to="/logs" className={linkCls}>看使用日志</Link>,
      });
    }
  }
  const ok = s && issues.length === 0;
  const q = s?.quota;

  return (
    <div>
      <PageHeader
        title={me?.name ? `你好，${me.name}` : "我的状态"}
        desc="你的 AI 账号现在能不能用；不能用的话，原因和下一步都在这里。"
      />
      {status.isLoading && <Loading />}
      {status.error && <ErrorBox error={status.error} />}
      {s && (
        <div className="space-y-5">
          <Card className="p-5">
            <div className="flex items-center gap-3">
              <span
                className={
                  "inline-block h-3 w-3 rounded-full " +
                  (ok ? "bg-emerald-500" : issues.some((i) => i.tone === "rose") ? "bg-rose-500" : "bg-amber-500")
                }
              />
              <h2 className="text-base font-semibold text-slate-900">
                {ok ? "一切正常，可以直接用" : `有 ${issues.length} 件事需要处理`}
              </h2>
            </div>
            {ok && s.last_ok && (
              <p className="mt-2 text-sm text-slate-500">
                最近一次成功：{fmtAgo(s.last_ok.at)} · {s.last_ok.client_kind || "-"} ·{" "}
                <span className="font-mono">{s.last_ok.model || "-"}</span>
              </p>
            )}
            {!ok && (
              <ul className="mt-4 space-y-3">
                {issues.map((i, n) => (
                  <li
                    key={n}
                    className={"flex flex-wrap items-center justify-between gap-3 rounded-lg border px-4 py-3 " + TONES[i.tone]}
                  >
                    <div>
                      <div className="text-sm font-medium">{i.title}</div>
                      {i.detail && <div className="mt-0.5 text-xs opacity-80">{i.detail}</div>}
                    </div>
                    {i.action}
                  </li>
                ))}
              </ul>
            )}
          </Card>

          <div className="grid gap-4 sm:grid-cols-2">
            <Card className="p-5">
              <div className="text-xs font-medium text-slate-400">今天的请求</div>
              <div className="mt-1 text-2xl font-semibold tabular-nums text-slate-900">
                {fmtNum(s.today_requests)}
              </div>
              <Link to="/usage" className="mt-2 inline-block text-xs text-indigo-600 hover:underline">
                看用量明细
              </Link>
            </Card>
            <Card className="p-5">
              <div className="text-xs font-medium text-slate-400">最近一次成功</div>
              <div className="mt-1 text-2xl font-semibold text-slate-900">
                {s.last_ok ? fmtAgo(s.last_ok.at) : "还没有"}
              </div>
              <Link to="/logs" className="mt-2 inline-block text-xs text-indigo-600 hover:underline">
                看使用日志
              </Link>
            </Card>
          </div>

          {s.account.bound && (
            <Card className="p-5">
              <h2 className="mb-3 text-sm font-semibold text-slate-800">账号额度</h2>
              {q ? (
                <div className="grid gap-4 sm:grid-cols-2">
                  <Meter
                    label="5 小时额度已用"
                    pct={q.PrimaryUsedPct}
                    hint={q.PrimaryResetAt ? `${fmtWhen(q.PrimaryResetAt)} 重置` : undefined}
                  />
                  <Meter
                    label="每周额度已用"
                    pct={q.SecondaryUsedPct}
                    hint={q.SecondaryResetAt ? `${fmtWhen(q.SecondaryResetAt)} 重置` : undefined}
                  />
                </div>
              ) : (
                <p className="text-sm text-slate-400">暂无额度数据，用过一次后就有了。</p>
              )}
              <div className="mt-4 border-t border-slate-100 pt-3">
                <Switch
                  checked={s.quota_alert}
                  onChange={(v) => reminder.mutate(v)}
                  disabled={reminder.isPending}
                  label="额度用到 80% 时，在飞书提醒我（只发给你自己）"
                />
              </div>
            </Card>
          )}
        </div>
      )}
    </div>
  );
}
