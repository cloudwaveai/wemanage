import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { ConfigSnippets } from "../lib/types";
import { Card, PageHeader, Loading, ErrorBox } from "../components/ui";
import { CodeBlock, CopyButton } from "../components/CopyButton";
import { CCSWITCH_DOWNLOAD, ccSwitchLink, looksLikeKey } from "../lib/ccswitch";
import { InstallCommand } from "../components/InstallCommand";

export function ConfigPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["config-snippets"],
    queryFn: () => api.get<ConfigSnippets>("/api/me/config-snippets"),
  });

  return (
    <div>
      <PageHeader
        title="一键配置"
        desc="推荐用一条命令配好；也可以导入 CC Switch，或按下面的手动配置写入 Codex。"
      />

      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}

      {data && <ConfigBody data={data} />}
    </div>
  );
}

// ConfigBody holds the key and model the employee enters once; every way of
// configuring below uses them.
function ConfigBody({ data }: { data: ConfigSnippets }) {
  const [key, setKey] = useState("");
  const [model, setModel] = useState(data.default_model);
  const keyOK = looksLikeKey(key);
  const manual = data.codex_toml
    .split("{{KEY}}").join(keyOK ? key.trim() : "在上面粘贴你的 Key 后这里会自动填上")
    .split("{{MODEL}}").join(model);
  return (
    <div className="space-y-5">
      <SetupCard data={data} keyValue={key} setKey={setKey} model={model} setModel={setModel} />

      <h2 className="pt-2 text-sm font-semibold text-slate-500">
        手动配置（前两种都不方便时）
      </h2>

      <Card className="p-5">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-sm font-semibold text-slate-800">
            Codex <span className="font-mono">config.toml</span>
          </h2>
        </div>
        {!keyOK && (
          <p className="mb-3 rounded-lg bg-amber-50 px-4 py-2.5 text-sm text-amber-800">
            先在上面「你的 Key」里粘贴 Key，下面的配置才会带上它，复制出来就能直接用。
          </p>
        )}
        <CodeBlock code={manual} />
        <p className="mt-2 text-xs leading-relaxed text-slate-500">
          写入 <code className="font-mono">~/.codex/config.toml</code>（Windows：
          <code className="font-mono">%USERPROFILE%\.codex\config.toml</code>），原来的 model_provider、model
          两行要删掉。Key 直接写在文件里，不用再设环境变量，Codex 桌面版也能用；这个文件别发给别人。
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
  );
}

// SetupCard takes the employee's key and model once, then offers the two ways
// to set Codex up with them: a one-line install command (the key goes to the
// gateway only to make a single-use code) or a CC Switch import (the key goes
// only into the link handed to the local app).
function SetupCard({
  data,
  keyValue: key,
  setKey,
  model,
  setModel,
}: {
  data: ConfigSnippets;
  keyValue: string;
  setKey: (v: string) => void;
  model: string;
  setModel: (v: string) => void;
}) {
  const models = data.models.length
    ? data.models
    : [{ id: data.default_model, name: "", created: 0 }];
  const keyOK = looksLikeKey(key);
  const link = keyOK && model ? ccSwitchLink(data.ccswitch, key, model) : "";
  const tools = !!data.ccswitch.config_toml;
  const inputCls =
    "w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";
  return (
    <Card className="p-5">
      <h2 className="text-sm font-semibold text-slate-800">配置 Codex</h2>
      <p className="mt-1 text-sm text-slate-500">
        粘贴你的 Key、选好模型，然后任选一种方式。
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

      <div className="mt-5 border-t border-slate-100 pt-4">
        <h3 className="mb-2 text-sm font-semibold text-slate-800">
          方式一：一条命令配好（推荐）
        </h3>
        <InstallCommand keyValue={keyOK ? key : ""} model={model} tools={tools} />
      </div>

      <div className="mt-5 border-t border-slate-100 pt-4">
        <h3 className="text-sm font-semibold text-slate-800">方式二：导入 CC Switch</h3>
        <p className="mt-1 text-xs leading-relaxed text-slate-500">
          适合经常在几个服务商之间来回切换的人。点「导入到 CC Switch」，CC Switch 弹窗确认后 Codex 就切到公司网关；Key
          只写进你电脑上的 CC Switch。
          {tools && "注意：CC Switch 导入的配置没有生图和联网搜索，要用的话按下面替换一次。"}
        </p>
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <a
            href={link || undefined}
            aria-disabled={!link}
            className={
              "inline-flex items-center rounded-lg px-3.5 py-2 text-sm font-medium " +
              (link
                ? "border border-indigo-200 bg-white text-indigo-700 hover:bg-indigo-50"
                : "pointer-events-none border border-slate-200 bg-white text-slate-300")
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
        {tools && (
          <details className="mt-3 rounded-lg border border-slate-200 bg-slate-50/60 p-3">
            <summary className="cursor-pointer text-xs font-medium text-slate-600">
              导入后开启生图和联网搜索
            </summary>
            <p className="mt-2 text-xs leading-relaxed text-slate-500">
              在 CC Switch 里点这个供应商的「编辑」，把 config.toml 整段换成下面这段（已经填好你的 Key
              和模型），保存后重新打开 Codex。
            </p>
            <div className="mt-2">
              <CodeBlock
                code={data.ccswitch.config_toml
                  .replace("{{KEY}}", keyOK ? key.trim() : "粘贴你的 sk-emp- Key")
                  .replace("{{MODEL}}", model)}
              />
            </div>
          </details>
        )}
      </div>

      <ul className="mt-5 list-disc space-y-1 border-t border-slate-100 pl-5 pt-4 text-xs text-slate-500">
        <li>
          Key 只在生成时显示一次。忘了的话到「我的 Key」重置一个，新 Key 生成时会直接给出安装命令。
        </li>
        {!data.provider && (
          <li className="text-amber-600">
            你还没有绑定账号，配好后暂时用不了；管理员分配好后飞书会通知你。
          </li>
        )}
        <li>
          电脑上开着代理的话，把 <code className="font-mono">{hostOf(data.ccswitch.homepage)}</code>{" "}
          加到代理的直连名单，否则每次请求会多绕几秒（方式一会自动处理终端里的代理设置）。
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
