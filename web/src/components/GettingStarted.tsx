import React, { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "../context/AuthContext";
import { Button, Card } from "./ui";
import { CopyButton } from "./CopyButton";
import { fmtDateTime } from "../lib/format";
import type { Me } from "../lib/types";

// Getting started: what a new employee does, in order, and how, each step
// ticked off from what the gateway knows (account, key, a request that got
// through). It never blocks anything: it can be put away and reopened from
// the header's 使用指引.

const HIDDEN = "aiseat_guide_hidden";
const INSTALLED = "aiseat_codex_installed";

function readFlag(k: string): boolean {
  try {
    return localStorage.getItem(k) === "1";
  } catch {
    return false;
  }
}

function writeFlag(k: string, on: boolean) {
  try {
    if (on) localStorage.setItem(k, "1");
    else localStorage.removeItem(k);
  } catch {
    /* storage unavailable */
  }
}

export function guideHidden(): boolean {
  return readFlag(HIDDEN);
}

// older reports whether dotted version a is older than b ("0.150.2" < "0.157.1").
function older(a: string, b: string): boolean {
  const pa = a.split(".").map((x) => parseInt(x, 10));
  const pb = b.split(".").map((x) => parseInt(x, 10));
  if (pa.some(isNaN) || pb.some(isNaN)) return false;
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const d = (pa[i] ?? 0) - (pb[i] ?? 0);
    if (d !== 0) return d < 0;
  }
  return false;
}

// staleCodex: the employee's Codex CLI is older than the release the gateway
// targets (the desktop app numbers its versions differently, so it is not compared).
function staleCodex(me: Me): boolean {
  const u = me.last_success;
  return !!(u?.cli && u.version && me.codex_version && older(u.version, me.codex_version));
}

function Cmd({ text }: { text: string }) {
  return (
    <div className="mt-1.5 flex items-center gap-2">
      <code className="scroll-thin flex-1 overflow-x-auto whitespace-nowrap rounded-md bg-slate-900 px-3 py-1.5 font-mono text-xs text-emerald-300">
        {text}
      </code>
      <CopyButton text={text} />
    </div>
  );
}

// OpenTerminal: nobody is assumed to know where the terminal is.
function OpenTerminal() {
  return (
    <p className="rounded-md bg-slate-50 px-2.5 py-1.5 text-slate-500">
      怎么打开终端：macOS 按 ⌘ + 空格，输入“终端”后回车；Windows 点开始菜单，输入“PowerShell”后回车。
    </p>
  );
}

interface Step {
  title: string;
  done: boolean;
  byAdmin?: boolean; // done by an administrator, not by the employee
  status?: React.ReactNode; // what the gateway sees, when done
  how: React.ReactNode;
  action?: React.ReactNode;
}

function StepRow({ n, step, current }: { n: number; step: Step; current: boolean }) {
  const [open, setOpen] = useState(current);
  useEffect(() => setOpen(current), [current]);
  return (
    <li className="flex gap-3">
      <div
        className={
          "mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold " +
          (step.done
            ? "bg-emerald-500 text-white"
            : current
            ? "bg-indigo-600 text-white"
            : "bg-slate-200 text-slate-500")
        }
      >
        {step.done ? (
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3">
            <path d="M5 12l5 5L20 7" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        ) : (
          n
        )}
      </div>
      <div className="min-w-0 flex-1 pb-4">
        <button
          onClick={() => setOpen((o) => !o)}
          className="flex w-full items-center justify-between gap-2 text-left"
        >
          <span className={"text-sm font-medium " + (step.done ? "text-slate-500" : "text-slate-800")}>
            {step.title}
            {step.byAdmin && !step.done && (
              <span className="ml-2 rounded bg-amber-100 px-1.5 py-0.5 text-[11px] font-normal text-amber-700">等管理员</span>
            )}
          </span>
          <span className="shrink-0 text-xs text-slate-400">{open ? "收起" : "怎么做"}</span>
        </button>
        {step.done && step.status && <div className="mt-0.5 text-xs text-emerald-700">{step.status}</div>}
        {open && (
          <div className="mt-2 space-y-2 text-xs leading-relaxed text-slate-600">
            {step.how}
            {!step.done && step.action && <div className="pt-1">{step.action}</div>}
          </div>
        )}
      </div>
    </li>
  );
}

const TIPS: { to: string; title: string; text: string }[] = [
  { to: "/usage", title: "我的用量", text: "看 5 小时和每周额度用了多少、什么时候恢复；用到 80% 和用完时飞书会提醒你。有重置卡的话可以按需使用。" },
  { to: "/logs", title: "使用日志", text: "每次请求的状态和用量，失败时写明原因，Codex 报错时先来这里看。" },
  { to: "/devices", title: "我的设备", text: "换新电脑第一次用时要在这里确认；不是你本人的设备就点「不是我」，Key 会立即作废。" },
  { to: "/reports", title: "我的周报", text: "每周日下午根据你本周在 Codex 里做的事自动写好周报，飞书发给你，复制后改一改就能交。" },
];

function Tips() {
  return (
    <div>
      <div className="grid gap-2 sm:grid-cols-2">
        {TIPS.map((t) => (
          <Link
            key={t.to}
            to={t.to}
            className="rounded-lg border border-slate-200 px-3 py-2.5 transition-colors hover:border-indigo-200 hover:bg-indigo-50/40"
          >
            <div className="text-sm font-medium text-slate-800">{t.title}</div>
            <div className="mt-0.5 text-xs leading-relaxed text-slate-500">{t.text}</div>
          </Link>
        ))}
      </div>
      <p className="mt-3 text-xs leading-relaxed text-slate-500">
        选模型：日常任务用 luna，复杂任务用 sol；Astra 消耗额度最快，确有需要时再临时切换，就像不必请资深专家做基础工作。
        省额度：一个任务做完就新建对话；别让 Codex 通读大文件、完整日志或依赖目录。
      </p>
    </div>
  );
}

function steps(me: Me, installed: boolean, setInstalled: (v: boolean) => void, props: GuideProps): Step[] {
  const used = me.last_success;
  const host = window.location.hostname;
  const toKey = (label: string) => (
    <Link
      to="/key"
      onClick={props.onNavigate}
      className="inline-flex items-center rounded-lg bg-indigo-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-indigo-500"
    >
      {label}
    </Link>
  );
  const stale = staleCodex(me);
  return [
    {
      title: "管理员分配 AI 账号",
      done: me.accounts.length > 0,
      byAdmin: true,
      status: "已分配",
      how: <p>管理员给你分配好 AI 账号后，你会收到飞书通知。这一步不用你操作，可以先做下面几步。</p>,
    },
    {
      title: "安装 Codex",
      done: installed || !!used,
      status: used ? "已检测到 Codex" : "你已确认装好",
      how: (
        <>
          <OpenTerminal />
          <p>
            ① 先装 Node.js：到
            <a href="https://nodejs.org/zh-cn" target="_blank" rel="noreferrer" className="mx-0.5 text-indigo-600 underline underline-offset-2">
              nodejs.org
            </a>
            下载 LTS 版本，双击安装、一路“下一步”即可。装好后在终端运行 <code className="font-mono">node -v</code>，显示 v18 或更高的版本号就行。
          </p>
          <div>
            ② 再装 Codex，在终端运行：
            <Cmd text="npm install -g @openai/codex" />
          </div>
          <div>
            ③ 检查：在终端运行下面这条，显示出版本号就说明装好了。
            <Cmd text="codex --version" />
          </div>
          <p>也可以用 ChatGPT 桌面版的「工作」（就是 Codex）或 VS Code 里的 Codex 插件，它们和命令行读同一份配置；不过下一步的安装命令仍要在终端里运行一次。ChatGPT 桌面版的「聊天」不走公司网关。</p>
        </>
      ),
      action: (
        <Button onClick={() => setInstalled(true)}>我已装好</Button>
      ),
    },
    {
      title: "生成 Key",
      done: !!me.key_prefix,
      status: me.key_prefix ? `当前 Key：${me.key_prefix}…` : undefined,
      how: <p>点「生成 Key」。Key 只完整显示一次，请复制保存好；弹窗里会直接给出下一步的安装命令。</p>,
      action: props.onIssueKey ? (
        <Button variant="primary" onClick={props.onIssueKey} disabled={props.issuing}>
          {props.issuing ? "生成中…" : "生成 Key"}
        </Button>
      ) : (
        toKey("去生成 Key")
      ),
    },
    {
      title: "把 Codex 接到公司网关",
      done: !!used,
      status: "已接通",
      how: (
        <>
          <p>
            复制生成 Key 弹窗里的安装命令，粘贴到终端里回车，看到“完成”后重新打开 Codex（ChatGPT 桌面版要按 ⌘Q 完全退出再打开）。
            命令 10 分钟内有效、只能用一次，里面不含 Key 本身。
          </p>
          <OpenTerminal />
          <p>弹窗已经关掉了？在「已有 Key，在另一台电脑上配置」里粘贴保存的 Key，就能重新拿到命令；Key 没保存的话只能重置。</p>
        </>
      ),
      action: me.key_prefix ? (
        props.onShowSetup ? (
          <Button variant="primary" onClick={props.onShowSetup}>
            获取安装命令
          </Button>
        ) : (
          toKey("获取安装命令")
        )
      ) : undefined,
    },
    {
      title: "检查：在 Codex 里发一条消息",
      done: !!used,
      status: used && (
        <>
          {fmtDateTime(used.at)} 通过{used.client || "Codex"}
          {used.version ? ` ${used.version}` : ""} 成功请求，配置正常。
          {stale && (
            <span className="mt-1 block text-amber-700">
              你的 Codex 版本较旧，建议升级到最新版：
              <Cmd text="npm install -g @openai/codex@latest" />
            </span>
          )}
        </>
      ),
      how: (
        <>
          <div>
            打开 Codex 随便问一句，或者在终端运行下面这条。能正常回复就说明配置成功，这一步会自动打勾。
            <Cmd text='codex exec "你好"' />
          </div>
          <p>
            没打勾？先看
            <Link to="/logs" onClick={props.onNavigate} className="mx-0.5 text-indigo-600 underline underline-offset-2">
              使用日志
            </Link>
            里最新一条的失败原因。常见的有：新电脑待确认（到「我的设备」确认）、Key 已重置（重新运行安装命令）、终端开着代理（把 {host} 设为直连）。
          </p>
        </>
      ),
    },
  ];
}

interface GuideProps {
  onIssueKey?: () => void; // generate the key in place (我的 Key)
  issuing?: boolean;
  onShowSetup?: () => void; // open "已有 Key，在另一台电脑上配置"
  onNavigate?: () => void; // a link was followed (closes the modal)
  onHide?: () => void; // put the guide away; absent in the modal
}

// GettingStartedBody is the checklist and the tips, for the page and the modal.
export function GettingStartedBody(props: GuideProps) {
  const { me, refetch } = useAuth();
  const [installed, setInstalledState] = useState(() => readFlag(INSTALLED));
  const setInstalled = (v: boolean) => {
    writeFlag(INSTALLED, v);
    setInstalledState(v);
  };
  const waiting = !!me?.key_prefix && !me?.last_success;
  // Waiting for the first request to get through: look again every 10 s.
  useEffect(() => {
    if (!waiting) return;
    const t = window.setInterval(refetch, 10_000);
    return () => window.clearInterval(t);
  }, [waiting, refetch]);

  if (!me) return null;
  const list = steps(me, installed, setInstalled, props);
  const done = list.filter((s) => s.done).length;
  // The step to do next: the employee's own, not one waiting on an admin.
  const current = list.findIndex((s) => !s.done && !s.byAdmin);
  const all = done === list.length;

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-slate-100">
          <div
            className="h-full rounded-full bg-emerald-500 transition-all"
            style={{ width: `${(done / list.length) * 100}%` }}
          />
        </div>
        <span className="shrink-0 text-xs text-slate-500">
          {done}/{list.length}
        </span>
      </div>
      {all ? (
        <>
          <p className="mb-4 rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
            全部配好了，可以直接在 Codex 里用。下面是之后常用的几个地方。
          </p>
          {staleCodex(me) && (
            <div className="mb-4 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800">
              你的 Codex 版本（{me.last_success?.version}）较旧，部分功能（比如联网搜索）可能用不了，建议升级：
              <Cmd text="npm install -g @openai/codex@latest" />
            </div>
          )}
        </>
      ) : (
        <ol>
          {list.map((s, i) => (
            <StepRow key={s.title} n={i + 1} step={s} current={i === current} />
          ))}
        </ol>
      )}
      {all ? (
        <Tips />
      ) : (
        <details className="rounded-lg border border-slate-200 px-3 py-2">
          <summary className="cursor-pointer text-sm font-medium text-slate-700">配好之后可以做什么</summary>
          <div className="mt-3">
            <Tips />
          </div>
        </details>
      )}
      {all && (
        <details className="mt-3 text-xs text-slate-500">
          <summary className="cursor-pointer">再看一遍配置步骤</summary>
          <ol className="mt-3">
            {list.map((s, i) => (
              <StepRow key={s.title} n={i + 1} step={s} current={false} />
            ))}
          </ol>
        </details>
      )}
    </div>
  );
}

// GettingStarted is the guide as a card at the top of 我的 Key.
export function GettingStarted(props: Omit<GuideProps, "onHide" | "onNavigate">) {
  const [hidden, setHidden] = useState(guideHidden);
  if (hidden) return null;
  const hide = () => {
    writeFlag(HIDDEN, true);
    setHidden(true);
  };
  return (
    <Card className="mb-5 p-5">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-slate-800">开始使用</h2>
          <p className="mt-0.5 text-xs text-slate-500">按顺序做完这几步就能在 Codex 里用公司的 AI 账号，每步做完会自动打勾。</p>
        </div>
        <button onClick={hide} className="shrink-0 text-xs text-slate-400 hover:text-slate-600">
          收起
        </button>
      </div>
      <GettingStartedBody {...props} />
      <p className="mt-3 text-[11px] text-slate-400">收起后可以随时从右上角「使用指引」打开。</p>
    </Card>
  );
}
