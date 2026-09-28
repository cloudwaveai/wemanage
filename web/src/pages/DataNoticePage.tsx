import { useAuth } from "../context/AuthContext";
import { Card, PageHeader } from "../components/ui";

// DataNoticePage tells employees plainly what the gateway records, who can
// see it and for how long. Nothing to acknowledge.
export function DataNoticePage() {
  const { me } = useAuth();
  return (
    <div>
      <PageHeader title="数据说明" desc="网关记录了什么、谁能看到、保存多久。" />
      <div className="space-y-5">
        <Card className="p-5">
          <div className="whitespace-pre-wrap text-sm leading-relaxed text-slate-700">
            {me?.notice_text || "管理员还没有填写数据说明。"}
          </div>
        </Card>
        <Card className="p-5">
          <h2 className="mb-3 text-sm font-semibold text-slate-800">谁能看到什么</h2>
          <ul className="list-disc space-y-1.5 pl-5 text-sm text-slate-600">
            <li>你的对话记录：你自己在「对话记录」里随时能看；超级管理员也能看，每次查看都会写入审计。</li>
            <li>部门负责人：只看到团队的总用量，以及成员账号是否正常，看不到个人用量和对话内容。</li>
            <li>周报：只发给你本人，可以在「我的周报」里关掉。</li>
            <li>设备和登录 IP：只用来发现 Key 被别人使用，保护你的账号。</li>
          </ul>
        </Card>
      </div>
    </div>
  );
}
