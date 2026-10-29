import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import { Button, Card, ErrorBox, Loading, PageHeader } from "../components/ui";
import { useToast } from "../components/Toast";

export type APIChannel = { id: number; name: string; protocol: "openai" | "anthropic"; base_url: string; active: boolean };

export function ChannelsPage() {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState<"openai" | "anthropic">("openai");
  const [baseURL, setBaseURL] = useState("");
  const [credential, setCredential] = useState("");
  const query = useQuery({ queryKey: ["api-channels"], queryFn: () => api.get<{ channels: APIChannel[] }>("/api/admin/api-channels") });
  const create = useMutation({
    mutationFn: () => api.post("/api/admin/api-channels", { name: name.trim(), protocol, base_url: baseURL.trim(), credential }),
    onSuccess: () => { setName(""); setBaseURL(""); setCredential(""); qc.invalidateQueries({ queryKey: ["api-channels"] }); toast("渠道已创建", "success"); },
    onError: (e: unknown) => toast(e instanceof Error ? e.message : "创建失败", "error"),
  });
  const toggle = useMutation({
    mutationFn: (c: APIChannel) => api.patch(`/api/admin/api-channels/${c.id}`, { active: !c.active }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["api-channels"] }),
    onError: (e: unknown) => toast(e instanceof Error ? e.message : "操作失败", "error"),
  });
  const input = "mt-1 w-full rounded-lg border border-slate-300 px-3 py-2 text-sm";
  return <div>
    <PageHeader title="API 渠道" desc="为应用配置 OpenAI 或 Anthropic 协议的上游。密钥加密存储，渠道密钥与其他账号隔离。" />
    <Card className="p-5">
      <h2 className="mb-3 text-sm font-semibold">新增上游渠道</h2>
      <div className="grid gap-4 md:grid-cols-2">
        <label className="text-sm">名称<input className={input} value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder="例如 项目模型服务" /></label>
        <label className="text-sm">协议<select className={input} value={protocol} onChange={(e) => setProtocol(e.target.value as "openai" | "anthropic")}><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option></select></label>
        <label className="text-sm">基础地址<input className={input} value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder="https://api.example.com/v1" /></label>
        <label className="text-sm">上游 API 密钥<input className={input} type="password" autoComplete="off" value={credential} onChange={(e) => setCredential(e.target.value)} /></label>
      </div>
      <p className="mt-3 text-xs text-slate-500">填写上游实际基础地址，通常以 /v1 结尾。仅支持 HTTPS 公网地址。调用会产生上游费用。</p>
      <Button className="mt-4" variant="primary" disabled={!name.trim() || !baseURL.trim() || !credential || create.isPending} onClick={() => create.mutate()}>{create.isPending ? "保存中…" : "创建渠道"}</Button>
    </Card>
    <Card className="mt-5 p-5">
      <h2 className="mb-3 text-sm font-semibold">已配置渠道</h2>
      {query.isLoading && <Loading />}{query.error && <ErrorBox error={query.error} />}
      {query.data?.channels.length === 0 && <p className="text-sm text-slate-500">还没有渠道。</p>}
      <div className="space-y-2">{query.data?.channels.map((c) => <div key={c.id} className="rounded-lg border border-slate-200 p-3 text-sm"><div className="flex flex-wrap items-center justify-between gap-3"><div><div className="font-medium">{c.name} · {c.protocol === "openai" ? "OpenAI" : "Anthropic"} · {c.active ? "启用" : "停用"}</div><div className="mt-1 text-xs text-slate-500">{c.base_url}</div></div><Button disabled={toggle.isPending} onClick={() => toggle.mutate(c)}>{c.active ? "停用" : "启用"}</Button></div><ChannelCredential channel={c}/><ChannelRates channel={c}/></div>)}</div>
      <p className="mt-4 text-sm text-slate-600">创建渠道后，到 <Link className="text-indigo-600 underline" to="/employees">员工 → 服务账号 → 管理密钥</Link> 创建绑定该渠道的应用密钥。</p>
    </Card>
  </div>;
}

type Rate = { model: string; currency: string; input_per_million: number; cached_per_million: number; output_per_million: number; effective_at: string };
function ChannelRates({ channel }: { channel: APIChannel }) {
  const { toast } = useToast(); const qc = useQueryClient();
  const [model,setModel]=useState(""); const [currency,setCurrency]=useState("CNY"); const [input,setInput]=useState(""); const [cached,setCached]=useState(""); const [output,setOutput]=useState(""); const [effective,setEffective]=useState("");
  const rates=useQuery({queryKey:["channel-rates",channel.id],queryFn:()=>api.get<{rates:Rate[]}>(`/api/admin/api-channels/${channel.id}/rates`)});
  const add=useMutation({mutationFn:()=>api.post(`/api/admin/api-channels/${channel.id}/rates`,{model:model.trim(),currency,input_per_million:Number(input),cached_per_million:Number(cached),output_per_million:Number(output),effective_at:new Date(effective).toISOString()}),onSuccess:()=>{setModel("");setInput("");setCached("");setOutput("");setEffective("");qc.invalidateQueries({queryKey:["channel-rates",channel.id]});toast("价格已保存","success")},onError:(e:unknown)=>toast(e instanceof Error?e.message:"保存失败","error")});
  const cls="w-full rounded border border-slate-300 px-2 py-1.5 text-sm";
  return <details className="mt-3"><summary className="cursor-pointer text-sm text-indigo-600">模型价格</summary><p className="mt-2 text-xs text-slate-500">每百万 Token 的估算单价。填写全部三项；不适用的缓存价格请按上游实际口径填写。价格按生效时间匹配，不能代替账单。</p>
   {rates.error&&<ErrorBox error={rates.error}/>}<div className="mt-2 space-y-1 text-xs text-slate-600">{rates.data?.rates.map((r,i)=><div key={i}>{r.model} · {r.currency} 输入 {r.input_per_million} / 缓存 {r.cached_per_million} / 输出 {r.output_per_million} · {new Date(r.effective_at).toLocaleString("zh-CN")} 起</div>)}</div>
   <div className="mt-3 grid gap-2 sm:grid-cols-3"><label className="text-xs">模型<input className={cls} value={model} onChange={e=>setModel(e.target.value)} /></label><label className="text-xs">币种<select className={cls} value={currency} onChange={e=>setCurrency(e.target.value)}><option>CNY</option><option>USD</option></select></label><label className="text-xs">生效时间<input className={cls} type="datetime-local" value={effective} onChange={e=>setEffective(e.target.value)} /></label>{[["输入单价",input,setInput],["缓存单价",cached,setCached],["输出单价",output,setOutput]].map(([label,value,setter])=><label key={label as string} className="text-xs">{label as string}<input className={cls} type="number" min="0" step="any" value={value as string} onChange={e=>(setter as (x:string)=>void)(e.target.value)} /></label>)}</div>
   <Button className="mt-3" disabled={!model.trim()||!effective||input===""||cached===""||output===""||add.isPending} onClick={()=>add.mutate()}>添加价格</Button>
  </details>;
}


function ChannelCredential({channel}:{channel:APIChannel}) {
 const [value,setValue]=useState("");const {toast}=useToast();
 const change=useMutation({mutationFn:()=>api.patch(`/api/admin/api-channels/${channel.id}`,{credential:value}),onSuccess:()=>{setValue("");toast("上游密钥已更新","success")},onError:(e:unknown)=>toast(e instanceof Error?e.message:"更新失败","error")});
 return <details className="mt-3"><summary className="cursor-pointer text-sm text-indigo-600">更换上游密钥</summary><p className="mt-1 text-xs text-slate-500">更新后，绑定此渠道的应用密钥保持不变；上游调用改用新密钥。</p><div className="mt-2 flex gap-2"><input type="password" autoComplete="off" className="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1.5 text-sm" value={value} onChange={e=>setValue(e.target.value)} placeholder="新上游密钥"/><Button disabled={!value.trim()||change.isPending} onClick={()=>change.mutate()}>更新</Button></div></details>;
}
