import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import { CopyButton } from "./CopyButton";

export interface InstallCommands {
  unix: string;
  windows: string;
  expires_at: string;
}

type OS = "unix" | "windows";

function guessOS(): OS {
  return /Windows/i.test(navigator.userAgent) ? "windows" : "unix";
}

// InstallCommand turns the employee's key into a one-line setup command. The
// command carries a single-use code, never the key, so it is safe in shell
// history. With auto, the command is made as soon as a key and model are
// given (right after a key is issued); otherwise on a click.
export function InstallCommand({
  keyValue,
  model,
  tools,
  auto = false,
}: {
  keyValue: string;
  model: string;
  tools: boolean; // the account can use Codex's image generation and web search
  auto?: boolean;
}) {
  const [os, setOS] = useState<OS>(guessOS);
  const make = useMutation({
    mutationFn: () =>
      api.post<InstallCommands>("/api/me/install-code", { key: keyValue.trim(), model }),
  });
  const cmds = make.data;
  const ready = !!keyValue.trim() && !!model;

  // A new key or model makes the earlier command stale.
  const { reset, mutate } = make;
  useEffect(() => {
    reset();
    if (auto && ready) mutate();
  }, [keyValue, model, auto, ready, reset, mutate]);

  const cmd = cmds ? (os === "windows" ? cmds.windows : cmds.unix) : "";
  const tab = (value: OS, label: string) => (
    <button
      type="button"
      onClick={() => setOS(value)}
      className={
        "rounded-md px-2.5 py-1 text-xs font-medium " +
        (os === value ? "bg-slate-900 text-white" : "text-slate-500 hover:bg-slate-100")
      }
    >
      {label}
    </button>
  );

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        {tab("unix", "macOS / Linux 终端")}
        {tab("windows", "Windows PowerShell")}
      </div>
      {cmds ? (
        <div className="mt-2 flex items-center gap-2">
          <code className="scroll-thin flex-1 overflow-x-auto whitespace-nowrap rounded-lg bg-slate-900 px-4 py-3 font-mono text-xs text-emerald-300">
            {cmd}
          </code>
          <CopyButton text={cmd} />
        </div>
      ) : (
        <div className="mt-2 flex flex-wrap items-center gap-3">
          <button
            type="button"
            onClick={() => make.mutate()}
            disabled={!ready || make.isPending}
            className="inline-flex items-center rounded-lg bg-indigo-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-indigo-500 disabled:bg-indigo-300"
          >
            {make.isPending ? "生成中…" : "生成安装命令"}
          </button>
          {!keyValue.trim() && (
            <span className="text-xs text-slate-400">先填上你的 Key</span>
          )}
        </div>
      )}
      {make.error && (
        <p className="mt-2 text-xs text-rose-600">
          {make.error instanceof Error ? make.error.message : "生成失败"}
        </p>
      )}
      <p className="mt-2 text-xs leading-relaxed text-slate-500">
        {os === "windows"
          ? "打开 PowerShell，粘贴回车。"
          : "打开「终端」，粘贴回车。"}
        会先备份你原来的 Codex 配置，只替换模型和网关这两部分，其它设置（MCP、项目信任等）都保留
        {tools ? "，生图和联网搜索也一并打开" : ""}。命令 10 分钟内有效、只能用一次，里面不含 Key 本身。
        {cmds && (
          <button
            type="button"
            onClick={() => make.mutate()}
            className="ml-1 text-indigo-600 hover:underline"
          >
            重新生成
          </button>
        )}
      </p>
    </div>
  );
}
