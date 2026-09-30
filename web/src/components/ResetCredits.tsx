import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { ResetCreditsResp } from "../lib/types";
import { Modal } from "./Modal";
import { useToast } from "./Toast";
import { Card, Button } from "./ui";

type Credit = ResetCreditsResp["credits"][number];

// ChatGPT names credits in English; the one kind seen so far is
// "Full reset (Weekly + 5 hr)".
function creditLabel(title: string): { name: string; scope: string } {
  if (/full/i.test(title)) return { name: "完整重置", scope: "5 小时 + 每周额度" };
  return { name: "重置卡", scope: title };
}

function expiry(at: string | null, now: Date): { date: string; left: string; soon: boolean } {
  if (!at) return { date: "长期有效", left: "", soon: false };
  const d = new Date(at);
  const date =
    (d.getFullYear() !== now.getFullYear() ? `${d.getFullYear()}年` : "") +
    `${d.getMonth() + 1}月${d.getDate()}日`;
  const days = Math.ceil((d.getTime() - now.getTime()) / 86_400_000);
  return { date: `${date}到期`, left: `还剩 ${Math.max(days, 0)} 天`, soon: days <= 3 };
}

function Ticket({ credit, first }: { credit: Credit; first: boolean }) {
  const { name, scope } = creditLabel(credit.title);
  const exp = expiry(credit.expires_at, new Date());
  return (
    <div className="relative flex overflow-hidden rounded-xl border border-indigo-200 bg-gradient-to-br from-indigo-50 to-white">
      <div className="flex w-14 shrink-0 items-center justify-center bg-indigo-600 text-white">
        <svg viewBox="0 0 24 24" className="h-6 w-6" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
          <path d="M3 12a9 9 0 0 1 15.5-6.2L21 8" />
          <path d="M21 3v5h-5" />
          <path d="M21 12a9 9 0 0 1-15.5 6.2L3 16" />
          <path d="M3 21v-5h5" />
        </svg>
      </div>
      {/* the notches that make it read as a ticket */}
      <span className="absolute -top-2 left-14 h-4 w-4 -translate-x-1/2 rounded-full border border-indigo-200 bg-white" />
      <span className="absolute -bottom-2 left-14 h-4 w-4 -translate-x-1/2 rounded-full border border-indigo-200 bg-white" />
      <div className="min-w-0 flex-1 px-4 py-3">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold text-slate-800">{name}</span>
          {first && (
            <span className="rounded bg-indigo-100 px-1.5 py-0.5 text-[11px] text-indigo-700">先用这张</span>
          )}
        </div>
        <div className="mt-0.5 text-xs text-slate-500">{scope}</div>
        <div className={"mt-2 text-xs " + (exp.soon ? "font-medium text-amber-600" : "text-slate-400")}>
          <span className="whitespace-nowrap">{exp.date}</span>
          {exp.left && <span className="whitespace-nowrap"> · {exp.left}</span>}
        </div>
      </div>
    </div>
  );
}

// ResetCreditsCard shows the reset credits (重置卡) of the employee's
// account, one ticket each, and spends the one that expires first.
export function ResetCreditsCard() {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [confirm, setConfirm] = useState(false);
  const credits = useQuery({
    queryKey: ["my-reset-credits"],
    queryFn: () => api.get<ResetCreditsResp>("/api/me/reset-credits"),
    retry: false,
  });
  const spend = useMutation({
    mutationFn: () => api.post<{ ok: boolean; message: string }>("/api/me/reset-credits/use"),
    onSuccess: (d) => {
      setConfirm(false);
      toast(d.message, d.ok ? "success" : "info");
      qc.invalidateQueries({ queryKey: ["my-quota"] });
      qc.invalidateQueries({ queryKey: ["my-reset-credits"] });
    },
    onError: (e: unknown) => {
      setConfirm(false);
      toast(e instanceof Error ? e.message : "重置没有成功", "error");
    },
  });

  const rc = credits.data;
  if (!rc?.supported) return null;
  const list = [...rc.credits].sort((a, b) =>
    (a.expires_at ?? "9999").localeCompare(b.expires_at ?? "9999"),
  );

  return (
    <Card className="p-5">
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="flex items-baseline gap-2 text-sm font-semibold text-slate-800">
            重置卡
            <span className="text-2xl font-semibold text-indigo-600">{rc.available}</span>
            <span className="text-sm font-normal text-slate-500">张</span>
          </h2>
          <p className="mt-1 text-xs text-slate-500">用一张，5 小时和每周额度立即清零、重新开始计算。</p>
        </div>
        <Button variant="primary" disabled={!rc.can_use || spend.isPending} onClick={() => setConfirm(true)}>
          使用一张
        </Button>
      </div>

      {list.length > 0 ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((c, i) => (
            <Ticket key={c.id} credit={c} first={i === 0 && list.length > 1} />
          ))}
        </div>
      ) : (
        <div className="rounded-xl border border-dashed border-slate-300 px-4 py-6 text-center text-sm text-slate-400">
          暂时没有重置卡，OpenAI 会不定期发放。
        </div>
      )}

      {list.length > 0 && !rc.can_use && rc.reason && (
        <p className="mt-3 text-xs text-slate-400">{rc.reason}</p>
      )}

      <Modal
        open={confirm}
        onClose={() => setConfirm(false)}
        title="使用重置卡？"
        footer={
          <>
            <Button onClick={() => setConfirm(false)}>取消</Button>
            <Button variant="primary" onClick={() => spend.mutate()} disabled={spend.isPending}>
              {spend.isPending ? "重置中…" : "确认使用"}
            </Button>
          </>
        }
      >
        <p className="text-sm leading-relaxed text-slate-600">
          使用后，你的 5 小时和每周额度会立即清零、重新开始计算。
          {list.length > 1 ? "会先用最早到期的那张。" : ""}
          重置卡用一张少一张，确定现在使用吗？
        </p>
      </Modal>
    </Card>
  );
}
