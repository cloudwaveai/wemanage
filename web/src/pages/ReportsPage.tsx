import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { Report } from "../lib/types";
import { Card, PageHeader, Loading, ErrorBox, Empty, Switch } from "../components/ui";
import { Markdown } from "../lib/md";
import { fmtNum, fmtDateTime } from "../lib/format";
import { useAuth } from "../context/AuthContext";
import { useToast } from "../components/Toast";
import { CopyButton } from "../components/CopyButton";

const STAT_LABELS: Record<string, string> = {
  requests: "请求",
  tokens_input: "输入 Token",
  tokens_output: "输出 Token",
};

function StatChips({ stats }: { stats: unknown }) {
  if (!stats || typeof stats !== "object") return null;
  const s = stats as Record<string, unknown>;
  const entries = Object.keys(STAT_LABELS).filter((k) => typeof s[k] === "number");
  if (!entries.length) return null;
  return (
    <div className="flex flex-wrap gap-2">
      {entries.map((k) => (
        <div key={k} className="rounded-lg bg-slate-50 px-3 py-1.5 text-xs">
          <span className="text-slate-400">{STAT_LABELS[k]}</span>{" "}
          <span className="font-semibold text-slate-700">{fmtNum(s[k] as number)}</span>
        </div>
      ))}
    </div>
  );
}

// weekLabel renders "09/21 – 09/27" for a report dated on the week's Monday.
function weekLabel(monday: string): string {
  const start = new Date(monday);
  const end = new Date(start.getTime() + 6 * 24 * 3600 * 1000);
  const f = (d: Date) =>
    `${String(d.getMonth() + 1).padStart(2, "0")}/${String(d.getDate()).padStart(2, "0")}`;
  return `${f(start)} – ${f(end)}`;
}

function ReportItem({ r }: { r: Report }) {
  const [open, setOpen] = useState(false);
  return (
    <Card className="overflow-hidden">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center justify-between px-5 py-4 text-left hover:bg-slate-50"
      >
        <div>
          <div className="text-sm font-semibold text-slate-900">{weekLabel(r.Date)}</div>
          <div className="mt-0.5 text-xs text-slate-400">生成于 {fmtDateTime(r.GeneratedAt)}</div>
        </div>
        <svg
          width="18"
          height="18"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          className={"text-slate-400 transition-transform " + (open ? "rotate-180" : "")}
        >
          <path d="M6 9l6 6 6-6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {open && (
        <div className="space-y-3 border-t border-slate-100 px-5 py-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <StatChips stats={r.Stats} />
            <CopyButton text={r.SummaryMD} label="复制周报" />
          </div>
          <Markdown text={r.SummaryMD} />
        </div>
      )}
    </Card>
  );
}

export function ReportsPage() {
  const { me, refetch } = useAuth();
  const { toast } = useToast();
  const { data, isLoading, error } = useQuery({
    queryKey: ["my-reports"],
    queryFn: () => api.get<{ reports: Report[] | null }>("/api/me/reports"),
  });
  const pref = useMutation({
    mutationFn: (enabled: boolean) => api.post("/api/me/weekly-report", { enabled }),
    onSuccess: (_, enabled) => {
      toast(enabled ? "已开启自动周报" : "已关闭自动周报，之后不会再生成和推送", "success");
      refetch();
    },
    onError: (e: unknown) => toast(e instanceof Error ? e.message : "操作失败", "error"),
  });

  const reports = data?.reports ?? [];
  const enabled = me?.weekly_report ?? true;

  return (
    <div>
      <PageHeader
        title="我的周报"
        desc="每周日下午根据你本周在 Codex 里做的事，自动编写本周周报，省去你编写周报的麻烦。只发给你本人，部门负责人和其他人都看不到。"
      />
      <Card className="mb-5 flex flex-wrap items-center justify-between gap-3 p-4">
        <div>
          <div className="text-sm font-medium text-slate-800">每周日自动编写本周周报</div>
          <div className="mt-0.5 text-xs text-slate-400">
            写好后通过飞书发给你，复制后稍作修改就能提交。关掉后不再生成，随时可以再打开。
          </div>
        </div>
        <Switch
          checked={pref.isPending ? !!pref.variables : enabled}
          disabled={pref.isPending}
          onChange={(v) => pref.mutate(v)}
          label="自动周报"
        />
      </Card>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && reports.length === 0 && <Empty label="还没有周报，本周日下午会自动写好" />}
      <div className="space-y-3">
        {reports.map((r) => (
          <ReportItem key={r.ID} r={r} />
        ))}
      </div>
    </div>
  );
}
