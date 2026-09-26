import { useState } from "react";

export function CopyButton({
  text,
  label = "复制",
  className = "",
}: {
  text: string;
  label?: string;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
      } else {
        // fallback for non-secure / older contexts
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      }
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  };
  return (
    <button
      onClick={copy}
      className={
        "inline-flex items-center gap-1 rounded-md border border-slate-300 bg-white px-2.5 py-1 text-xs font-medium text-slate-600 hover:bg-slate-50 " +
        className
      }
    >
      {copied ? "已复制 ✓" : label}
    </button>
  );
}

export function CodeBlock({
  code,
  copyLabel = "复制",
}: {
  code: string;
  copyLabel?: string;
}) {
  return (
    <div className="relative">
      <div className="absolute right-2 top-2">
        <CopyButton text={code} label={copyLabel} />
      </div>
      <pre className="scroll-thin overflow-x-auto rounded-lg bg-slate-900 p-4 pr-20 font-mono text-xs leading-relaxed text-slate-100">
        {code}
      </pre>
    </div>
  );
}
