import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { ConfigSnippets } from "../lib/types";
import { Card, PageHeader, Loading, ErrorBox } from "../components/ui";
import { CodeBlock, CopyButton } from "../components/CopyButton";

export function ConfigPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["config-snippets"],
    queryFn: () => api.get<ConfigSnippets>("/api/me/config-snippets"),
  });

  return (
    <div>
      <PageHeader
        title="一键配置"
        desc="将下面的配置写入 Codex，即可通过网关访问模型。"
      />

      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}

      {data && (
        <div className="space-y-5">
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
