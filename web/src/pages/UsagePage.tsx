import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../context/AuthContext";
import type { UsageSummary, QuotaResp, ResetCreditsResp } from "../lib/types";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import {
  Card,
  PageHeader,
  StatTile,
  Loading,
  ErrorBox,
  BarList,
  StatusBadge,
  Meter,
  RangeTabs,
  Switch,
  Button,
} from "../components/ui";
import { fmtNum, fmtDateTime, fmtDate } from "../lib/format";
import { UsageTrend } from "../components/UsageTrend";

export function UsagePage() {
  const [range, setRange] = useState(7);
  const { me, refetch } = useAuth();
  const reminder = useMutation({
    mutationFn: (enabled: boolean) => api.post("/api/me/quota-alert", { enabled }),
    onSuccess: () => refetch(),
  });
  const qc = useQueryClient();
  const { toast } = useToast();
  const [confirmReset, setConfirmReset] = useState(false);
  const credits = useQuery({
    queryKey: ["my-reset-credits"],
    queryFn: () => api.get<ResetCreditsResp>("/api/me/reset-credits"),
    retry: false,
  });
  const spendCredit = useMutation({
    mutationFn: () => api.post<{ ok: boolean; message: string }>("/api/me/reset-credits/use"),
    onSuccess: (d) => {
      setConfirmReset(false);
      toast(d.message, d.ok ? "success" : "info");
      qc.invalidateQueries({ queryKey: ["my-quota"] });
      qc.invalidateQueries({ queryKey: ["my-reset-credits"] });
    },
    onError: (e: unknown) => {
      setConfirmReset(false);
      toast(e instanceof Error ? e.message : "重置没有成功", "error");
    },
  });
  const rc = credits.data;
  const soonest = rc?.credits
    .map((c) => c.expires_at)
    .filter((x): x is string => !!x)
    .sort()[0];

  const usage = useQuery({
    queryKey: ["my-usage", range],
    queryFn: () => api.get<UsageSummary>(`/api/me/usage?range=${range}`),
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  });

  const quota = useQuery({
    queryKey: ["my-quota"],
    queryFn: async () => {
      try {
        return await api.get<QuotaResp>("/api/me/quota");
      } catch (e) {
        // 404 = no account bound; treat as "no quota" rather than an error.
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    retry: false,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
  });

  const u = usage.data;
  const totalTokens = u
    ? u.tokens_input + u.tokens_output + u.tokens_reasoning
    : 0;


  return (
    <div>
      <PageHeader
        title="我的用量"
        desc="你的请求量与 token 消耗统计。"
        right={<RangeTabs value={range} onChange={setRange} />}
      />

      {usage.isLoading && <Loading />}
      {usage.error && <ErrorBox error={usage.error} />}

      {u && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <StatTile label="请求数" value={fmtNum(u.requests)} />
            <StatTile label="总 Token" value={fmtNum(totalTokens)} />
            <StatTile label="输入 Token" value={fmtNum(u.tokens_input)} />
            <StatTile label="输出 Token" value={fmtNum(u.tokens_output)} />
          </div>

          {/* quota */}
          {quota.data && (
            <Card className="p-5">
              <div className="mb-3 flex items-center justify-between">
                <h2 className="text-sm font-semibold text-slate-800">账号额度</h2>
                <StatusBadge status={quota.data.status} />
              </div>
              {quota.data.quota ? (
                <div className="grid gap-4 sm:grid-cols-2">
                  <Meter
                    label="5 小时额度已用"
                    pct={quota.data.quota.PrimaryUsedPct}
                    hint={
                      quota.data.quota.PrimaryResetAt
                        ? `重置于 ${fmtDateTime(quota.data.quota.PrimaryResetAt)}`
                        : undefined
                    }
                  />
                  <Meter
                    label="每周额度已用"
                    pct={quota.data.quota.SecondaryUsedPct}
                    hint={
                      quota.data.quota.SecondaryResetAt
                        ? `重置于 ${fmtDateTime(quota.data.quota.SecondaryResetAt)}`
                        : undefined
                    }
                  />
                </div>
              ) : (
                <p className="text-sm text-slate-400">暂无额度快照。</p>
              )}
              {rc?.supported && (
                <div className="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-lg bg-slate-50 px-4 py-3">
                  <div>
                    <div className="text-sm font-medium text-slate-800">重置卡 {rc.available} 张</div>
                    <div className="mt-0.5 text-xs text-slate-500">
                      {rc.available > 0
                        ? `用一张，5 小时和每周额度立即重新开始${soonest ? `；最早一张 ${fmtDate(soonest)} 过期` : ""}。`
                        : "暂时没有重置卡。"}
                      {rc.available > 0 && !rc.can_use && rc.reason}
                    </div>
                  </div>
                  <Button
                    variant="primary"
                    disabled={!rc.can_use || spendCredit.isPending}
                    onClick={() => setConfirmReset(true)}
                  >
                    使用重置卡
                  </Button>
                </div>
              )}
              <div className="mt-4 border-t border-slate-100 pt-3">
                <Switch
                  checked={!!me?.quota_alert}
                  onChange={(v) => reminder.mutate(v)}
                  disabled={reminder.isPending}
                  label="额度用到 80% 和用完时，在飞书提醒我（只发给你自己）"
                />
              </div>
            </Card>
          )}

          <Card className="p-5">
            <h2 className="mb-1 text-sm font-semibold text-slate-800">请求趋势</h2>
            <UsageTrend series={u.series} />
          </Card>

          {/* breakdowns */}
          <div className="grid gap-4 md:grid-cols-2">
            <Card className="p-5">
              <h2 className="mb-3 text-sm font-semibold text-slate-800">
                按模型
              </h2>
              <BarList
                items={(u.by_model ?? []).map((b) => ({
                  label: b.key,
                  value: b.requests,
                  sub: `${fmtNum(b.requests)} 次 · ${fmtNum(b.tokens_total)} tok`,
                }))}
              />
            </Card>
            <Card className="p-5">
              <h2 className="mb-3 text-sm font-semibold text-slate-800">
                按客户端
              </h2>
              <BarList
                items={(u.by_client ?? []).map((b) => ({
                  label: b.key,
                  value: b.requests,
                  sub: `${fmtNum(b.requests)} 次 · ${fmtNum(b.tokens_total)} tok`,
                }))}
              />
            </Card>
          </div>
        </div>
      )}

      <Modal
        open={confirmReset}
        onClose={() => setConfirmReset(false)}
        title="使用重置卡？"
        footer={
          <>
            <Button onClick={() => setConfirmReset(false)}>取消</Button>
            <Button variant="primary" onClick={() => spendCredit.mutate()} disabled={spendCredit.isPending}>
              {spendCredit.isPending ? "重置中…" : "确认使用"}
            </Button>
          </>
        }
      >
        <p className="text-sm leading-relaxed text-slate-600">
          使用后，你的 5 小时和每周额度会立即清零、重新开始计算。重置卡用一张少一张，确定现在使用吗？
        </p>
      </Modal>
    </div>
  );
}
