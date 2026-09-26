import { useMutation } from "@tanstack/react-query";
import { useAuth } from "../context/AuthContext";
import { useToast } from "./Toast";
import { api } from "../lib/api";

// Full-screen first-run notice. The employee cannot use the app until they
// acknowledge it. Renders as an overlay above whatever else is mounted.
export function NoticeGate() {
  const { me, refetch } = useAuth();
  const { toast } = useToast();
  const ack = useMutation({
    mutationFn: () => api.post("/api/me/ack-notice"),
    onSuccess: () => refetch(),
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "操作失败", "error"),
  });

  if (!me || me.notice_acked) return null;

  return (
    <div className="fixed inset-0 z-[80] flex items-center justify-center bg-slate-900/60 p-4">
      <div className="w-full max-w-lg rounded-2xl bg-white p-6 shadow-2xl">
        <h2 className="text-lg font-semibold text-slate-900">使用告知</h2>
        <div className="mt-4 max-h-[50vh] overflow-y-auto whitespace-pre-wrap rounded-lg bg-slate-50 p-4 text-sm leading-relaxed text-slate-700">
          {me.notice_text || "请阅读并确认使用告知后继续。"}
        </div>
        <div className="mt-6 flex justify-end">
          <button
            onClick={() => ack.mutate()}
            disabled={ack.isPending}
            className="rounded-lg bg-indigo-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          >
            {ack.isPending ? "提交中…" : "我已知晓"}
          </button>
        </div>
      </div>
    </div>
  );
}
