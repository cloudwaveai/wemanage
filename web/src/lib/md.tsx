import React from "react";

// A deliberately tiny, dependency-free Markdown renderer. It covers the subset
// that daily-report summaries use: headings, bold, inline code, fenced code
// blocks, unordered/ordered lists and paragraphs. Everything is rendered as
// React nodes (no dangerouslySetInnerHTML), so untrusted text cannot inject
// markup.

function renderInline(text: string, keyBase: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  // Split on **bold** and `code` while keeping delimiters.
  const re = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) nodes.push(text.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith("**")) {
      nodes.push(
        <strong key={`${keyBase}-b${i}`} className="font-semibold">
          {tok.slice(2, -2)}
        </strong>
      );
    } else {
      nodes.push(
        <code
          key={`${keyBase}-c${i}`}
          className="rounded bg-slate-100 px-1 py-0.5 font-mono text-[0.85em] text-slate-800"
        >
          {tok.slice(1, -1)}
        </code>
      );
    }
    last = m.index + tok.length;
    i++;
  }
  if (last < text.length) nodes.push(text.slice(last));
  return nodes;
}

export function Markdown({ text }: { text: string }) {
  if (!text || !text.trim()) {
    return <p className="text-sm text-slate-400">（无内容）</p>;
  }
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const blocks: React.ReactNode[] = [];
  let list: { ordered: boolean; items: string[] } | null = null;
  let para: string[] = [];
  let code: string[] | null = null;
  let k = 0;

  const flushPara = () => {
    if (para.length) {
      blocks.push(
        <p key={`p${k++}`} className="my-2 text-sm leading-relaxed text-slate-700">
          {renderInline(para.join(" "), `p${k}`)}
        </p>
      );
      para = [];
    }
  };
  const flushList = () => {
    if (list) {
      const items = list.items.map((it, idx) => (
        <li key={idx} className="my-0.5 text-sm leading-relaxed text-slate-700">
          {renderInline(it, `li${k}-${idx}`)}
        </li>
      ));
      blocks.push(
        list.ordered ? (
          <ol key={`l${k++}`} className="my-2 list-decimal pl-6">
            {items}
          </ol>
        ) : (
          <ul key={`l${k++}`} className="my-2 list-disc pl-6">
            {items}
          </ul>
        )
      );
      list = null;
    }
  };

  for (const raw of lines) {
    const line = raw;
    // fenced code block toggle
    if (line.trim().startsWith("```")) {
      if (code === null) {
        flushPara();
        flushList();
        code = [];
      } else {
        blocks.push(
          <pre
            key={`code${k++}`}
            className="scroll-thin my-2 overflow-x-auto rounded-lg bg-slate-900 p-3 font-mono text-xs text-slate-100"
          >
            {code.join("\n")}
          </pre>
        );
        code = null;
      }
      continue;
    }
    if (code !== null) {
      code.push(line);
      continue;
    }

    const heading = /^(#{1,4})\s+(.*)$/.exec(line);
    if (heading) {
      flushPara();
      flushList();
      const level = heading[1].length;
      const cls =
        level <= 1
          ? "text-lg font-semibold"
          : level === 2
          ? "text-base font-semibold"
          : "text-sm font-semibold";
      blocks.push(
        <div key={`h${k++}`} className={`mb-1 mt-3 text-slate-800 ${cls}`}>
          {renderInline(heading[2], `h${k}`)}
        </div>
      );
      continue;
    }

    const ul = /^\s*[-*+]\s+(.*)$/.exec(line);
    const ol = /^\s*\d+\.\s+(.*)$/.exec(line);
    if (ul || ol) {
      flushPara();
      const ordered = !!ol;
      const item = (ul ? ul[1] : ol![1]).trim();
      if (!list || list.ordered !== ordered) {
        flushList();
        list = { ordered, items: [] };
      }
      list.items.push(item);
      continue;
    }

    if (!line.trim()) {
      flushPara();
      flushList();
      continue;
    }
    // plain paragraph line
    flushList();
    para.push(line.trim());
  }
  // trailing flushes
  if (code !== null) {
    blocks.push(
      <pre
        key={`code${k++}`}
        className="scroll-thin my-2 overflow-x-auto rounded-lg bg-slate-900 p-3 font-mono text-xs text-slate-100"
      >
        {code.join("\n")}
      </pre>
    );
  }
  flushPara();
  flushList();

  return <div className="prose-sm">{blocks}</div>;
}
