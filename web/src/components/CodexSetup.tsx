import { useState } from "react";
import type { ConfigSnippets } from "../lib/types";
import { CodeBlock, CopyButton } from "./CopyButton";
import { InstallCommand } from "./InstallCommand";
import { CCSWITCH_DOWNLOAD, ccSwitchLink, looksLikeKey } from "../lib/ccswitch";

// CodexSetup sets Codex up with a key: a model to pick, the one-line install
// command, and, folded away, the other ways (CC Switch, the config file by
// hand, third-party clients). It shows right after a key is issued, and for
// a key the employee pasted (setting up another computer without a reset).
export function CodexSetup({
  data,
  keyValue,
  auto = false,
}: {
  data: ConfigSnippets;
  keyValue: string;
  auto?: boolean; // make the install command without a click
}) {
  const [model, setModel] = useState(data.default_model);
  const models = data.models.length
    ? data.models
    : [{ id: data.default_model, name: "", created: 0 }];
  const key = keyValue.trim();
  const keyOK = looksLikeKey(key);
  const tools = !!data.ccswitch.config_toml;
  const link = keyOK && model ? ccSwitchLink(data.ccswitch, key, model) : "";
  const fill = (tmpl: string) =>
    tmpl
      .split("{{KEY}}")
      .join(keyOK ? key : "你的 Key")
      .split("{{MODEL}}")
      .join(model);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="text-slate-500">模型</span>
        <select
          value={model}
          onChange={(e) => setModel(e.target.value)}
          className="rounded-lg border border-slate-300 px-2.5 py-1.5 text-sm outline-none focus:border-indigo-400"
        >
          {models.map((m) => (
            <option key={m.id} value={m.id}>
              {m.id}
              {m.id === data.default_model ? "（推荐）" : ""}
            </option>
          ))}
        </select>
        {!data.provider && (
          <span className="text-xs text-amber-600">还没分配 AI 账号，配好后要等管理员分配才能用。</span>
        )}
      </div>
      {/astra/i.test(model) && (
        <p className="-mt-2 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800">
          Astra 消耗额度最快，不建议设为默认模型。日常任务用 luna、复杂任务用 sol 就够了，确有需要时再在 Codex 里临时切换到 Astra。
        </p>
      )}

      <div className="rounded-lg border border-indigo-100 bg-indigo-50/60 px-4 py-3">
        <div className="mb-2 text-sm font-medium text-slate-800">一条命令配好 Codex（推荐）</div>
        <InstallCommand keyValue={keyOK ? key : ""} model={model} tools={tools} auto={auto} />
      </div>

      <details className="rounded-lg border border-slate-200 px-4 py-3">
        <summary className="cursor-pointer text-sm font-medium text-slate-700">
          其他方式：CC Switch、手动配置、OpenCode、Command Code
        </summary>

        <div className="mt-3 space-y-4 text-xs leading-relaxed text-slate-500">
          <div>
            <div className="mb-1 font-medium text-slate-700">CC Switch</div>
            <div className="flex flex-wrap items-center gap-3">
              <a
                href={link || undefined}
                aria-disabled={!link}
                className={
                  "font-medium " +
                  (link ? "text-indigo-600 hover:underline" : "pointer-events-none text-slate-300")
                }
              >
                一键导入 CC Switch
              </a>
              <a href={CCSWITCH_DOWNLOAD} target="_blank" rel="noreferrer" className="hover:underline">
                下载 CC Switch
              </a>
            </div>
            {tools && (
              <p className="mt-1">
                CC Switch 导入的配置没有生图和联网搜索。要用的话，在 CC Switch 里编辑这个供应商，把 config.toml
                换成下面「手动配置」这一段。
              </p>
            )}
          </div>

          <div>
            <div className="mb-1 font-medium text-slate-700">手动配置</div>
            <p className="mb-2">
              写入 <code className="font-mono">~/.codex/config.toml</code>（Windows：
              <code className="font-mono">%USERPROFILE%\.codex\config.toml</code>），原来的 model_provider、model
              两行删掉。Key 已经填在里面，这个文件别发给别人。
            </p>
            <CodeBlock code={fill(data.codex_toml)} />
          </div>

          <div>
            <div className="mb-1 font-medium text-slate-700">第三方客户端（OpenCode、Command Code 等）</div>
            <div className="flex flex-wrap items-center gap-2">
              <span>Base URL</span>
              <code className="rounded bg-slate-100 px-2 py-0.5 font-mono text-slate-700">
                {data.third_party.base_url}
              </code>
              <CopyButton text={data.third_party.base_url} />
              <span className="text-slate-400">接口类型选 OpenAI Responses</span>
            </div>
            <p className={"mt-1 " + (data.third_party.allowed ? "" : "text-amber-700")}>{data.third_party.note}</p>
            {data.third_party.allowed && (
              <div className="mt-3 space-y-3">
                <div>
                  <div className="mb-1 font-medium text-slate-700">OpenCode</div>
                  <p className="mb-1">
                    写入 <code className="font-mono">~/.config/opencode/opencode.json</code>（已有这个文件的话，把 provider 里的
                    aiseat 一段合并进去）。Key 已经填在里面，这个文件别发给别人。
                  </p>
                  <CodeBlock code={fill(data.third_party.opencode_json)} />
                </div>
                <div>
                  <div className="mb-1 font-medium text-slate-700">Command Code</div>
                  <p className="mb-1">
                    写入 <code className="font-mono">~/.commandcode/providers.json</code>，再把 Key 设成环境变量（下面这行加到
                    <code className="font-mono"> ~/.zshrc</code> 或 <code className="font-mono">~/.bashrc</code>，新开终端生效），然后在 Command Code 里用
                    <code className="font-mono"> /model</code> 选「公司 AI 网关」的模型。
                  </p>
                  <CodeBlock code={fill(data.third_party.commandcode_json)} />
                  <div className="mt-2">
                    <CodeBlock code={fill(data.third_party.commandcode_env)} />
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>
      </details>
    </div>
  );
}
