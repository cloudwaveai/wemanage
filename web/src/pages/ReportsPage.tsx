import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { DailyReport } from "../lib/types";
import { Card, PageHeader, Loading, ErrorBox, Empty } from "../components/ui";
import { Markdown } from "../lib/md";
import { fmtDate, fmtNum, fmtDateTime } from "../lib/format";

function StatChips({ stats }: { stats: unknown }) {
  if (!stats || typeof stats !== "object") return null;
  const entries = Object.entries(stats as Record<string, unknown>).filter(
    ([, v]) => typeof v === "number" || typeof v === "string"
  );
  if (!entries.length) return null;
  return (
    <div className="mt-3 flex flex-wrap gap-2">
      {entries.slice(0, 8).map(([k, v]) => (
        <div
          key={k}
          className="rounded-lg bg-slate-50 px-3 py-1.5 text-xs"
        >
          <span className="text-slate-400">{k}</span>{" "}
          <span className="font-semibold text-slate-700">
            {typeof v === "number" ? fmtNum(v) : String(v)}
          </span>
        </div>
      ))}
    </div>
  );
}

function ReportItem({ r }: { r: DailyReport }) {
  const [open, setOpen] = useState(false);
  return (
    <Card className="overflow-hidden">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center justify-between px-5 py-4 text-left hover:bg-slate-50"
      >
        <div>
          <div className="text-sm font-semibold text-slate-900">
            {fmtDate(r.Date)}
          </div>
          <div className="mt-0.5 text-xs text-slate-400">
            {r.Model || "—"} · 生成于 {fmtDateTime(r.GeneratedAt)}
          </div>
        </div>
        <svg
          width="18"
          height="18"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          className={
            "text-slate-400 transition-transform " + (open ? "rotate-180" : "")
          }
        >
          <path d="M6 9l6 6 6-6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {open && (
        <div className="border-t border-slate-100 px-5 py-4">
          <StatChips stats={r.Stats} />
          <div className="mt-3">
            <Markdown text={r.SummaryMD} />
          </div>
        </div>
      )}
    </Card>
  );
}

export function ReportsPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["my-reports"],
    queryFn: () => api.get<{ reports: DailyReport[] | null }>("/api/me/reports"),
  });

  const reports = data?.reports ?? [];

  return (
    <div>
      <PageHeader title="我的日报" desc="由系统自动生成的每日工作摘要。" />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && reports.length === 0 && <Empty label="暂无日报" />}
      <div className="space-y-3">
        {reports.map((r) => (
          <ReportItem key={r.ID} r={r} />
        ))}
      </div>
    </div>
  );
}
