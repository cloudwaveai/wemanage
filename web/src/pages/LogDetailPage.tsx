import type React from "react";
import { useQuery } from "@tanstack/react-query";
import { useParams, Link } from "react-router-dom";
import { api } from "../lib/api";
import type { LogDetail, TranscriptSegment } from "../lib/types";
import { Card, PageHeader, Loading, ErrorBox } from "../components/ui";
import { fmtDateTime, fmtNum } from "../lib/format";

function prettyData(data: unknown): string {
  if (data === null || data === undefined) return "";
  if (typeof data === "string") {
    // try to pretty-print JSON strings, else show raw
    try {
      return JSON.stringify(JSON.parse(data), null, 2);
    } catch {
      return data;
    }
  }
  try {
    return JSON.stringify(data, null, 2);
  } catch {
    return String(data);
  }
}

const KIND_LABEL: Record<string, string> = {
  params: "参数",
  instructions: "指令",
  tools: "工具",
  input: "输入",
  output: "输出",
};

function Segment({ seg, idx }: { seg: TranscriptSegment; idx: number }) {
  return (
    <div className="rounded-lg border border-slate-200">
      <div className="flex items-center gap-2 border-b border-slate-100 px-3 py-2">
        <span className="inline-flex items-center rounded-full bg-indigo-50 px-2 py-0.5 text-xs font-medium text-indigo-700">
          {KIND_LABEL[seg.kind] ?? seg.kind}
        </span>
        <span className="font-mono text-[11px] text-slate-400">
          #{idx + 1} {seg.kind}
        </span>
      </div>
      <pre className="scroll-thin max-h-96 overflow-auto whitespace-pre-wrap break-words px-3 py-2.5 font-mono text-xs leading-relaxed text-slate-700">
        {prettyData(seg.data)}
      </pre>
    </div>
  );
}

function SegmentList({
  title,
  segments,
}: {
  title: string;
  segments: TranscriptSegment[] | null;
}) {
  const list = segments ?? [];
  return (
    <Card className="p-5">
      <h2 className="mb-3 text-sm font-semibold text-slate-800">
        {title}
        <span className="ml-2 text-xs font-normal text-slate-400">
        {list.length} 段
        </span>
      </h2>
      {list.length ? (
        <div className="space-y-3">
          {list.map((s, i) => (
            <Segment key={i} seg={s} idx={i} />
          ))}
        </div>
      ) : (
        <p className="text-sm text-slate-400">无内容</p>
      )}
    </Card>
  );
}

export function LogDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, error } = useQuery({
    queryKey: ["log", id],
    queryFn: () => api.get<LogDetail>(`/api/logs/${id}`),
    enabled: !!id,
  });

  return (
    <div>
      <div className="mb-2">
        <Link
          to="/logs"
          className="text-sm font-medium text-indigo-600 hover:text-indigo-500"
        >
          ← 返回日志列表
        </Link>
      </div>
      <PageHeader title="请求详情" desc="重建的请求与响应原文。" />

      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}

      {data && (
        <div className="space-y-5">
          <Card className="p-5">
            <div className="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
              <Field label="时间" value={fmtDateTime(data.started_at)} />
              <Field label="客户端" value={data.client_kind || "-"} />
              <Field
                label="端点"
                value={<span className="font-mono text-xs">{data.endpoint}</span>}
              />
              <Field label="状态码" value={String(data.status_code || "-")} />
              <Field
                label="模型"
                value={
                  <span className="font-mono text-xs">
                    {data.model_requested || "-"}
                    {data.model_responded &&
                      data.model_responded !== data.model_requested &&
                      ` → ${data.model_responded}`}
                  </span>
                }
              />
              <Field label="输入 Token" value={fmtNum(data.tokens.input)} />
              <Field label="输出 Token" value={fmtNum(data.tokens.output)} />
              <Field
                label="缓存 / 推理"
                value={`${fmtNum(data.tokens.cached)} / ${fmtNum(
                  data.tokens.reasoning
                )}`}
              />
            </div>
          </Card>

          {data.transcript.purged ? (
            <Card className="p-8 text-center">
              <p className="text-sm text-slate-500">原文已按保留策略清除</p>
            </Card>
          ) : (
            <>
              <SegmentList title="请求" segments={data.transcript.request} />
              <SegmentList title="响应" segments={data.transcript.response} />
            </>
          )}
        </div>
      )}
    </div>
  );
}

function Field({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <div className="text-xs font-medium uppercase tracking-wide text-slate-400">
        {label}
      </div>
      <div className="mt-0.5 text-slate-800">{value}</div>
    </div>
  );
}
