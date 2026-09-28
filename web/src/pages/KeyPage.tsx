import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useAuth } from "../context/AuthContext";
import { api } from "../lib/api";
import { Card, PageHeader, Button } from "../components/ui";
import { CopyButton } from "../components/CopyButton";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { fmtDateTime } from "../lib/format";
import type { ConfigSnippets } from "../lib/types";
import { CCSWITCH_DOWNLOAD, ccSwitchLink } from "../lib/ccswitch";

export function KeyPage() {
  const { me, refetch } = useAuth();
  const { toast } = useToast();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [plainKey, setPlainKey] = useState<string | null>(null);

  const hasKey = !!me?.key_prefix;
  // Needed only to offer the CC Switch import next to a freshly issued key.
  const snippets = useQuery({
    queryKey: ["config-snippets"],
    queryFn: () => api.get<ConfigSnippets>("/api/me/config-snippets"),
    enabled: !!plainKey,
  });

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
        {plainKey && snippets.data && (
          <div className="mt-4 rounded-lg border border-indigo-100 bg-indigo-50/60 px-4 py-3">
            <div className="flex flex-wrap items-center gap-3">
              <a
                href={ccSwitchLink(
                  snippets.data.ccswitch,
                  plainKey,
                  snippets.data.default_model,
                )}
                className="inline-flex items-center rounded-lg bg-indigo-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-indigo-500"
              >
                一键导入 CC Switch
              </a>
              <a
                href={CCSWITCH_DOWNLOAD}
                target="_blank"
                rel="noreferrer"
                className="text-xs text-indigo-600 hover:underline"
              >
                还没装？下载 CC Switch
              </a>
            </div>
            <p className="mt-2 text-xs text-slate-500">
              使用模型 {snippets.data.default_model}，导入后 Codex 会切换到公司网关。要换模型，到「一键配置」页选择后重新导入。
            </p>
          </div>
        )}
      </Modal>
    </div>
  );
}
