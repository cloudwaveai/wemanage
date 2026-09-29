import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { ConfigSnippets } from "../lib/types";
import { Card, PageHeader, Loading, ErrorBox } from "../components/ui";
import { CodeBlock, CopyButton } from "../components/CopyButton";
import { CCSWITCH_DOWNLOAD, ccSwitchLink, looksLikeKey } from "../lib/ccswitch";

export function ConfigPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["config-snippets"],
    queryFn: () => api.get<ConfigSnippets>("/api/me/config-snippets"),
  });

  return (
    <div>
      <PageHeader
        title="一键配置"
        desc="推荐用 CC Switch 一键导入；不用 CC Switch 时，按下面的手动配置写入 Codex。"
      />

      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}

      {data && (
        <div className="space-y-5">
          <CCSwitchCard data={data} />

          <h2 className="pt-2 text-sm font-semibold text-slate-500">
            手动配置（不用 CC Switch 时）
          </h2>

          <Card className="p-5">
            <div className="mb-2 flex items-center justify-between">
              <h2 className="text-sm font-semibold text-slate-800">
                Codex <span className="font-mono">config.toml</span>
              </h2>
            </div>
            <CodeBlock code={data.codex_toml} />
          </Card>

          <Card className="p-5">
            <h2 className="mb-2 text-sm font-semibold text-slate-800">
              环境变量
            </h2>
            <CodeBlock code={data.codex_env} />
            <p className="mt-2 text-xs text-slate-500">
              将 API Key 换成你在「我的 Key」页面生成的 <code className="font-mono">sk-emp-</code> 开头密钥。
            </p>
          </Card>

          <Card className="p-5">
            <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
              <h2 className="text-sm font-semibold text-slate-800">
                第三方客户端
              </h2>
              <div className="flex items-center gap-2">
                <span className="font-mono text-xs text-slate-500">
                  Base URL
                </span>
                <code className="rounded bg-slate-100 px-2 py-0.5 font-mono text-xs text-slate-700">
                  {data.third_party.base_url}
                </code>
                <CopyButton text={data.third_party.base_url} />
              </div>
            </div>
            <div className="rounded-lg bg-amber-50 px-4 py-3 text-sm leading-relaxed text-amber-800">
              {data.third_party.note}
            </div>
            <p className="mt-2 text-xs font-medium text-amber-700">
              第三方客户端风险较高，建议优先使用 Codex。
            </p>
          </Card>
        </div>
      )}
    </div>
  );
}

// CCSwitchCard builds a ccswitch:// import link from the pasted key. The key
// stays in the browser: it only goes into the link handed to the local app.
function CCSwitchCard({ data }: { data: ConfigSnippets }) {
  const [key, setKey] = useState("");
  const [model, setModel] = useState(data.default_model);
  const models = data.models.length
    ? data.models
    : [{ id: data.default_model, name: "", created: 0 }];
  const keyOK = looksLikeKey(key);
  const link = keyOK && model ? ccSwitchLink(data.ccswitch, key, model) : "";
  const inputCls =
    "w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";

  return (
    <Card className="p-5">
      <h2 className="text-sm font-semibold text-slate-800">
        用 CC Switch 一键导入（推荐）
      </h2>
      <p className="mt-1 text-sm text-slate-500">
        粘贴你的 Key，选好模型，点「导入到 CC Switch」。CC Switch 会弹窗让你确认，确认后 Codex
        立即切换到公司网关。Key 只写进你电脑上的 CC Switch，不会再发回服务器。
      </p>

      <div className="mt-4 grid gap-3 sm:grid-cols-[1fr_220px]">
        <div>
          <label className="mb-1 block text-xs font-medium text-slate-500">
            你的 Key
          </label>
          <input
            type="password"
            autoComplete="off"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="sk-emp-…"
            className={inputCls + " font-mono"}
          />
          {key && !keyOK && (
            <p className="mt-1 text-xs text-rose-500">
              这不像是网关的 Key（应以 sk-emp- 开头）。
            </p>
          )}
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-slate-500">
            模型
          </label>
          <select
            value={model}
            onChange={(e) => setModel(e.target.value)}
            className={inputCls}
          >
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.id}
                {m.id === data.default_model ? "（推荐）" : ""}
                {m.name ? ` · ${m.name}` : ""}
              </option>
            ))}
          </select>
          {data.models_source === "catalog" && (
            <p className="mt-1 text-xs text-slate-400">
              你还没绑定账号，这里列的是 ChatGPT 账号通常能用的模型。
            </p>
          )}
        </div>
      </div>

      <div className="mt-4 flex flex-wrap items-center gap-3">
        <a
          href={link || undefined}
          aria-disabled={!link}
          className={
            "inline-flex items-center rounded-lg px-3.5 py-2 text-sm font-medium " +
            (link
              ? "bg-indigo-600 text-white hover:bg-indigo-500"
              : "pointer-events-none bg-indigo-300 text-white")
          }
        >
          导入到 CC Switch
        </a>
        <a
          href={CCSWITCH_DOWNLOAD}
          target="_blank"
          rel="noreferrer"
          className="text-sm text-indigo-600 hover:underline"
        >
          还没装？下载 CC Switch
        </a>
      </div>

      {data.ccswitch.config_toml && (
        <div className="mt-5 rounded-lg border border-indigo-100 bg-indigo-50/50 p-4">
          <h3 className="text-sm font-semibold text-slate-800">导入后开启生图和联网搜索</h3>
          <p className="mt-1 text-xs leading-relaxed text-slate-500">
            CC Switch 导入的配置会让 Codex 关掉生图（image_gen）和联网搜索。导入后在 CC Switch 里点这个供应商的「编辑」，
            把 config.toml 整段换成下面这段（已经填好你的 Key 和模型），保存后重新打开 Codex 即可。
          </p>
          <div className="mt-3">
            <CodeBlock
              code={data.ccswitch.config_toml
                .replace("{{KEY}}", keyOK ? key.trim() : "粘贴你的 sk-emp- Key")
                .replace("{{MODEL}}", model)}
            />
          </div>
        </div>
      )}

      <ul className="mt-4 list-disc space-y-1 pl-5 text-xs text-slate-500">
        <li>
          Key 只在生成时显示一次。忘了的话到「我的 Key」重置一个，新 Key 生成时也能直接一键导入。
        </li>
        {!data.provider && (
          <li className="text-amber-600">
            你还没有绑定账号，导入后暂时用不了，请先联系管理员分配账号。
          </li>
        )}
        <li>
          电脑上开着代理的话，把 <code className="font-mono">{hostOf(data.ccswitch.homepage)}</code>{" "}
          加到代理的直连名单，否则每次请求会多绕几秒。
        </li>
      </ul>
    </Card>
  );
}

function hostOf(u: string): string {
  try {
    return new URL(u).hostname;
  } catch {
    return u;
  }
}
