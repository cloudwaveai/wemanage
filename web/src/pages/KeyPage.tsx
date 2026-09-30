import { useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useAuth } from "../context/AuthContext";
import { api } from "../lib/api";
import { Card, PageHeader, Button } from "../components/ui";
import { CopyButton } from "../components/CopyButton";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { fmtDateTime } from "../lib/format";
import type { ConfigSnippets } from "../lib/types";
import { CodexSetup } from "../components/CodexSetup";
import { GettingStarted } from "../components/GettingStarted";

export function KeyPage() {
  const { me, refetch } = useAuth();
  const { toast } = useToast();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [plainKey, setPlainKey] = useState<string | null>(null);

  const hasKey = !!me?.key_prefix;
  const [pasted, setPasted] = useState("");
  const [setupOpen, setSetupOpen] = useState(false);
  const setupRef = useRef<HTMLDetailsElement>(null);
  // What every way of configuring Codex needs (models, config templates).
  const snippets = useQuery({
    queryKey: ["config-snippets"],
    queryFn: () => api.get<ConfigSnippets>("/api/me/config-snippets"),
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
        desc="你访问公司网关的专属密钥。生成后弹窗里直接给出配置 Codex 的方式。"
      />

      <GettingStarted
        onIssueKey={() => (hasKey ? setConfirmOpen(true) : issue.mutate())}
        issuing={issue.isPending}
        onShowSetup={() => {
          setSetupOpen(true);
          window.setTimeout(() => setupRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }), 0);
        }}
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
          Key 只在生成时完整显示一次，请妥善保存。重置会立即使旧 Key 失效，已经配好的电脑都要重新配置。
        </div>
      </Card>

      {hasKey && snippets.data && (
        <Card className="mt-5 p-5">
          <details
            ref={setupRef}
            className="scroll-mt-28"
            open={setupOpen}
            onToggle={(e) => setSetupOpen((e.target as HTMLDetailsElement).open)}
          >
            <summary className="cursor-pointer text-sm font-semibold text-slate-800">
              已有 Key，在另一台电脑上配置
            </summary>
            <p className="mt-2 text-xs text-slate-500">
              粘贴你保存的 Key，就能拿到这台电脑的安装命令，不用重置（重置会让其它电脑上的旧 Key 失效）。
            </p>
            <input
              type="password"
              autoComplete="off"
              value={pasted}
              onChange={(e) => setPasted(e.target.value)}
              placeholder="sk-emp-…"
              className="mt-3 w-full rounded-lg border border-slate-300 px-3 py-2 font-mono text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100"
            />
            <div className="mt-4">
              <CodexSetup data={snippets.data} keyValue={pasted} />
            </div>
          </details>
        </Card>
      )}

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
        width="max-w-2xl"
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
          <div className="mt-5">
            <CodexSetup data={snippets.data} keyValue={plainKey} auto />
          </div>
        )}
      </Modal>
    </div>
  );
}
