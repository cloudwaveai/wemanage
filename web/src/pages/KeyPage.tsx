import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useAuth } from "../context/AuthContext";
import { api } from "../lib/api";
import { Card, PageHeader, Button } from "../components/ui";
import { CopyButton } from "../components/CopyButton";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { fmtDateTime } from "../lib/format";

export function KeyPage() {
  const { me, refetch } = useAuth();
  const { toast } = useToast();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [plainKey, setPlainKey] = useState<string | null>(null);

  const hasKey = !!me?.key_prefix;

  const issue = useMutation({
    mutationFn: () => api.post<{ key: string; prefix: string }>("/api/me/key"),
    onSuccess: (d) => {
      setPlainKey(d.key);
      setConfirmOpen(false);
      refetch();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "操作失败", "error"),
  });

  return (
    <div>
      <PageHeader
        title="我的 Key"
        desc="用于在 Codex / 第三方客户端中访问网关的专属密钥。"
      />

      <Card className="p-5">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="text-xs font-medium uppercase tracking-wide text-slate-400">
              当前 Key
            </div>
            {hasKey ? (
              <div className="mt-1">
                <div className="font-mono text-lg text-slate-900">
                  {me?.key_prefix}
                  <span className="text-slate-400">………</span>
                </div>
                {me?.key_created_at && (
                  <div className="mt-1 text-xs text-slate-400">
                    创建于 {fmtDateTime(me.key_created_at)}
                  </div>
                )}
              </div>
            ) : (
              <div className="mt-1 text-sm text-slate-500">尚未生成 Key</div>
            )}
          </div>
          <Button
            variant="primary"
            onClick={() =>
              hasKey ? setConfirmOpen(true) : issue.mutate()
            }
            disabled={issue.isPending}
          >
            {issue.isPending
              ? "处理中…"
              : hasKey
              ? "重置 Key"
              : "生成 Key"}
          </Button>
        </div>

        <div className="mt-4 rounded-lg bg-amber-50 px-4 py-3 text-sm text-amber-800">
          Key 只在生成时完整显示一次，请妥善保存。重置会立即使旧 Key 失效。
        </div>
      </Card>

      {/* Confirm reset */}
      <Modal
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        title="确认重置 Key？"
        footer={
          <>
            <Button onClick={() => setConfirmOpen(false)}>取消</Button>
            <Button
              variant="danger"
              onClick={() => issue.mutate()}
              disabled={issue.isPending}
            >
              {issue.isPending ? "处理中…" : "确认重置"}
            </Button>
          </>
        }
      >
        <p className="text-sm text-slate-600">
          重置后当前 Key 将立即失效，所有正在使用旧 Key 的客户端都需要更新。此操作不可撤销。
        </p>
      </Modal>

      {/* Show plaintext once */}
      <Modal
        open={!!plainKey}
        onClose={() => setPlainKey(null)}
        title="你的新 Key"
        footer={
          <Button variant="primary" onClick={() => setPlainKey(null)}>
            我已保存
          </Button>
        }
      >
        <div className="rounded-lg bg-rose-50 px-4 py-3 text-sm text-rose-700">
          这是唯一一次完整展示，关闭后无法再次查看。请立即复制保存。
        </div>
        <div className="mt-3 flex items-center gap-2">
          <code className="scroll-thin flex-1 overflow-x-auto rounded-lg bg-slate-900 px-4 py-3 font-mono text-sm text-emerald-300">
            {plainKey}
          </code>
          {plainKey && <CopyButton text={plainKey} />}
        </div>
      </Modal>
    </div>
  );
}
