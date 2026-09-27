const pptxgen = require("pptxgenjs");
const p = new pptxgen();
p.layout = "LAYOUT_WIDE";

// ---- paper / engineering-blog palette ----
const BG="FBFCFE", INK="0F172A", BODY="334155", MUTE="64748B", FAINT="94A3B8";
const LINE="E2E8F0", CARD="FFFFFF";
const ACC="4F46E5", ACC2="0E9F9F", GOOD="15803D", WARN="B45309", BAD="B91C1C", PURP="6D28D9";
const CODEBG="0F172A", CFG="E2E8F0", CKEY="7DD3FC", CCMT="64748B", CSTR="86EFAC", CNUM="FBBF24";
const H="微软雅黑", MONO="Consolas";
const notes=[];

function slide(dark){ const s=p.addSlide(); s.background={color:dark?INK:BG}; return s; }
function kicker(s,t,c){ s.addText(t,{x:0.7,y:0.5,w:11.9,h:0.32,fontFace:H,fontSize:12,color:c||ACC,bold:true,charSpacing:3,isTextBox:true,margin:0}); }
function title(s,t,c){ s.addText(t,{x:0.7,y:0.84,w:11.9,h:0.7,fontFace:H,fontSize:27,bold:true,color:c||INK,isTextBox:true,margin:0}); }
function caption(s,x,y,w,n,t){ s.addText([
  {text:"图 "+n+"  ",options:{fontFace:H,fontSize:11,bold:true,color:ACC,breakLine:false}},
  {text:t,options:{fontFace:H,fontSize:11,color:MUTE,italic:true,breakLine:true}},
],{x,y,w,h:0.4,isTextBox:true,margin:0,valign:"top"}); }
function para(s,x,y,w,runs){ // runs: [{t,c?,b?}]
  s.addText(runs.map(r=>({text:r.t,options:{fontFace:H,fontSize:r.sz||13,color:r.c||BODY,bold:!!r.b,breakLine:r.nl!==false}})),
    {x,y,w,h:2,isTextBox:true,margin:0,valign:"top",lineSpacingMultiple:1.12}); }
function pageno(s,n){ s.addText(String(n).padStart(2,"0"),{x:12.4,y:7.0,w:0.6,h:0.28,align:"right",fontFace:MONO,fontSize:10,color:FAINT,isTextBox:true,margin:0}); }
function node(s,x,y,w,h,label,sub,fill,fg){ s.addShape(p.ShapeType.roundRect,{x,y,w,h,rectRadius:0.07,fill:{color:fill||CARD},line:{color:fill?fill:LINE,width:1}});
  s.addText(label,{x,y:sub?y+0.12:y,w,h:sub?0.4:h,align:"center",valign:sub?"top":"middle",fontFace:H,fontSize:13,bold:true,color:fg||INK,isTextBox:true,margin:0});
  if(sub) s.addText(sub,{x:x+0.08,y:y+0.5,w:w-0.16,h:h-0.55,align:"center",valign:"top",fontFace:H,fontSize:9.5,color:fg?fg:MUTE,isTextBox:true,margin:0}); }
function arrow(s,x1,y1,x2,y2,c,dash){ s.addShape(p.ShapeType.line,{x:Math.min(x1,x2),y:Math.min(y1,y2),w:Math.abs(x2-x1),h:Math.abs(y2-y1),
  line:{color:c||MUTE,width:1.5,endArrowType:"triangle",dashType:dash||"solid"},flipH:x2<x1,flipV:y2<y1}); }
function code(s,x,y,w,h,lines,ttl){ s.addShape(p.ShapeType.roundRect,{x,y,w,h,rectRadius:0.05,fill:{color:CODEBG}});
  if(ttl) s.addText(ttl,{x:x+0.2,y:y+0.1,w:w-0.4,h:0.26,fontFace:MONO,fontSize:10.5,color:CCMT,isTextBox:true,margin:0});
  const sy=y+(ttl?0.44:0.18);
  s.addText(lines.map(l=>({text:l.t,options:{fontFace:MONO,fontSize:l.sz||11,color:l.c||CFG,breakLine:true,bold:!!l.b}})),
    {x:x+0.2,y:sy,w:w-0.4,h:h-(sy-y)-0.12,isTextBox:true,margin:0,valign:"top",lineSpacingMultiple:1.05}); }

// ============ 1. TITLE ============
(()=>{ const s=slide(true);
  s.addText("ENGINEERING DEEP DIVE",{x:0.7,y:1.5,w:11,h:0.4,fontFace:MONO,fontSize:13,color:"818CF8",charSpacing:3,isTextBox:true,margin:0});
  s.addText("AI 订阅网关",{x:0.7,y:2.15,w:11.9,h:1.0,fontFace:H,fontSize:52,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
  s.addText("一次订阅代理网关的设计取舍：隔离、并发、日志与存储",{x:0.7,y:3.35,w:11.9,h:0.6,fontFace:H,fontSize:20,color:"CBD5E1",isTextBox:true,margin:0});
  para(s,0.7,4.35,11.4,[
    {t:"我们要把 AI 订阅统一发给员工，同时把用量、成本与合规握在公司手里，还不能让上游把账号判成异常。",c:"94A3B8",sz:13.5},
    {t:"这份文档记录其中真正难的部分——为什么这样设计、放弃了哪些方案、系统在故障下如何保持不变量。",c:"94A3B8",sz:13.5},
  ]);
  s.addText("Go 1.26 · PostgreSQL 16 · CLIProxyAPI · S3/R2 · Docker",{x:0.7,y:6.6,w:11,h:0.35,fontFace:MONO,fontSize:12,color:"475569",isTextBox:true,margin:0});
  notes.push("这是工程深潜版，不是宣讲。每页一张图 + 分析：为什么这么设计、权衡了什么、故障下的不变量是什么。");
})();

// ============ 2. PROBLEM & CONSTRAINTS ============
(()=>{ const s=slide(); kicker(s,"问题定义"); title(s,"约束决定形状");
  para(s,0.7,1.7,11.9,[
    {t:"“给每人一个订阅”很简单；难的是五条约束同时成立——它们互相拉扯，任何一条都能推翻朴素实现。",c:BODY,sz:13.5}]);
  const rows=[
    ["一人一号","每个 Key 只能打到本人账号，不做号池轮询","号池会让用量/责任无法归属，且更易触发风控",ACC],
    ["全量日志","请求与回复原文可留存、可分析","但直接存原文，Codex 每轮重发历史 → 体量爆炸",PURP],
    ["并发上限","每账号在途 ≤ 4","并发过高是账号被降智/风控的已知诱因",ACC2],
    ["指纹一致","上游看到的请求稳定、自洽","代理默认会改写头，反而制造“不自洽”破绽",WARN],
    ["合规留痕","按 PIPL 告知同意、限权、可清除","原文含源码与密钥，永久留存是负债",GOOD],
  ];
  let y=2.5; const x=0.7,w=11.9,hh=0.82;
  rows.forEach(r=>{ node(s,x,y,0.16,hh,"","",r[3]); s.addShape(p.ShapeType.roundRect,{x,y,w,h:hh,rectRadius:0.06,fill:{color:CARD},line:{color:LINE,width:1}});
    s.addShape(p.ShapeType.roundRect,{x,y,w:0.14,h:hh,rectRadius:0.04,fill:{color:r[3]}});
    s.addText(r[0],{x:x+0.4,y:y+0.1,w:2.1,h:hh-0.2,fontFace:H,fontSize:14,bold:true,color:INK,isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[1],{x:x+2.6,y:y+0.1,w:4.4,h:hh-0.2,fontFace:H,fontSize:11.5,color:BODY,isTextBox:true,margin:0,valign:"middle"});
    s.addText([{text:"张力：",options:{fontFace:H,fontSize:11,color:r[3],bold:true,breakLine:false}},{text:r[2],options:{fontFace:H,fontSize:11,color:MUTE,breakLine:true}}],
      {x:x+7.2,y:y+0.1,w:w-7.4,h:hh-0.2,fontFace:H,isTextBox:true,margin:0,valign:"middle"});
    y+=hh+0.14; });
  pageno(s,2);
  notes.push("五条约束互相拉扯：一人一号、全量日志、限并发、指纹一致、合规留痕。右列是每条带来的张力——正是这些张力塑造了后面的设计。");
})();

// ============ 3. DESIGN SPACE (decision) ============
(()=>{ const s=slide(); kicker(s,"关键决策"); title(s,"网关放在哪：三个方案的取舍");
  const cols=[
    ["A  自研网关在前 + 原版 CPA","选定",GOOD,[
      "公司逻辑全在自研网关，CPA 保持原版",
      "CPA 每天迭代 → 换镜像即可跟上游",
      "边界清晰、可独立测试",
      "代价：多一跳内网（毫秒级）"]],
    ["B  fork 并改造 CPA","否",BAD,[
      "直接改 CPA 源码塞公司逻辑",
      "每次上游更新都要重新合并冲突",
      "维护成本随时间发散",
      "—"]],
    ["C  把 CPA 当库嵌入","否",BAD,[
      "单进程、控制力最强",
      "与 CPA 内部 SDK 深度耦合",
      "v7 仍在快速变，升级即改我们代码",
      "—"]],
  ];
  const cw=3.86,gx=0.7,sx=0.24,y=2.35,hh=3.7;
  cols.forEach((c,i)=>{ const x=gx+i*(cw+sx); const sel=c[1]==="选定";
    s.addShape(p.ShapeType.roundRect,{x,y,w:cw,h:hh,rectRadius:0.08,fill:{color:CARD},line:{color:sel?GOOD:LINE,width:sel?2.5:1},shadow:sel?{type:"outer",color:"86EFAC",opacity:0.5,blur:10,offset:2,angle:90}:undefined});
    s.addText(c[0],{x:x+0.25,y:y+0.22,w:cw-0.5,h:0.7,fontFace:H,fontSize:14.5,bold:true,color:INK,isTextBox:true,margin:0});
    s.addShape(p.ShapeType.roundRect,{x:x+cw-1.05,y:y+0.2,w:0.8,h:0.34,rectRadius:0.06,fill:{color:c[2]}});
    s.addText(c[1],{x:x+cw-1.05,y:y+0.2,w:0.8,h:0.34,align:"center",valign:"middle",fontFace:H,fontSize:11,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
    s.addText(c[3].filter(t=>t!=="-").map(t=>({text:t,options:{bullet:{code:"2022",indent:14},fontFace:H,fontSize:11.5,color:BODY,breakLine:true,paraSpaceAfter:6}})),
      {x:x+0.28,y:y+1.05,w:cw-0.55,h:hh-1.2,isTextBox:true,margin:0,valign:"top"});
  });
  para(s,0.7,6.25,11.9,[{t:"判据：把“最常变的东西”隔离在最容易替换的边界后。CPA 是随上游协议变动最频繁的部分，所以让它保持原版、可整体替换——这条判据也贯穿后面每一处设计。",c:BODY,sz:12.5}]);
  pageno(s,3);
  notes.push("三个方案。选 A：公司逻辑放自研网关，CPA 保持原版跟上游。判据是把最易变的东西隔离在最易替换的边界后——CPA 随上游变得最勤，所以保持原版可整体替换。");
})();

// ============ 4. ARCHITECTURE + TRUST BOUNDARY ============
(()=>{ const s=slide(); kicker(s,"架构"); title(s,"信任边界与数据流");
  // trust zone dashed box around gateway+cpa+db
  s.addShape(p.ShapeType.roundRect,{x:3.05,y:2.15,w:9.5,h:4.1,rectRadius:0.1,fill:{color:"FFFFFF"},line:{color:ACC,width:1.5,dashType:"dash"}});
  s.addText("公司信任域（员工不可达 CPA / DB / 密钥）",{x:3.15,y:2.2,w:9.3,h:0.3,fontFace:H,fontSize:10.5,color:ACC,bold:true,isTextBox:true,margin:0});
  // client
  node(s,0.7,3.3,2.0,1.1,"员工客户端","Codex · Cursor\nchat/responses","8A93C0","FFFFFF");
  // gateway
  node(s,3.5,3.15,2.4,1.4,"Gateway (Go)","鉴权·并发·指纹\n日志·后台·SPA",ACC,"FFFFFF");
  // cpa
  node(s,6.55,3.3,2.0,1.1,"CLIProxyAPI","OAuth 刷新\n前缀路由","0E9F9F","FFFFFF");
  // openai
  node(s,11.0,3.3,1.35,1.1,"OpenAI","backend-api\n/codex",NAVYorInk(),"FFFFFF");
  // egress proxies
  node(s,9.05,3.35,1.55,1.0,"出口代理","分组固定 IP\nproxy_url","475569","FFFFFF");
  // stores
  node(s,3.5,5.0,2.4,1.0,"PostgreSQL","元数据·检索\n审计·配置","1E293B","FFFFFF");
  node(s,6.35,5.0,2.2,1.0,"对象存储 S3/R2","原文块(blobs)\n180 天","6D28D9","FFFFFF");
  // arrows
  arrow(s,2.7,3.85,3.5,3.85,MUTE);
  arrow(s,5.9,3.85,6.55,3.85,MUTE);
  arrow(s,8.55,3.85,9.05,3.85,MUTE);
  arrow(s,10.6,3.85,11.0,3.85,MUTE);
  arrow(s,4.7,4.55,4.7,5.0,ACC);          // gw -> pg
  arrow(s,5.0,4.55,6.9,5.0,PURP);         // gw -> blob (approx)
  s.addText("内网，员工到不了这一段",{x:6.4,y:4.55,w:3.5,h:0.3,fontFace:H,fontSize:9.5,italic:true,color:FAINT,isTextBox:true,margin:0});
  caption(s,0.7,6.35,11.9,1,"员工只持有公司签发的 Key 与地址；CPA、数据库、密钥、上游凭证都在信任域内。面向 OpenAI 的真实流量从每账号固定的出口代理发出，网关自身 IP 不直连上游。");
  pageno(s,4);
  notes.push("信任边界是关键：员工够不到 CPA/DB/密钥。面向 OpenAI 的是每账号固定出口代理，不是网关 IP。前缀路由的隔离之所以成立，正因为员工在边界之外。");
  function NAVYorInk(){ return "1E293B"; }
})();

// ============ 5. REQUEST SEQUENCE ============
(()=>{ const s=slide(); kicker(s,"数据面"); title(s,"一次流式请求的时序");
  const lanes=[["Client","8A93C0"],["Gateway","4F46E5"],["Limiter","0E9F9F"],["CPA","475569"],["OpenAI","1E293B"]];
  const xs=[1.5,4.0,6.4,8.8,11.3]; const top=2.35, bot=6.05;
  lanes.forEach((l,i)=>{ s.addShape(p.ShapeType.roundRect,{x:xs[i]-1.0,y:top,w:2.0,h:0.42,rectRadius:0.06,fill:{color:l[1]}});
    s.addText(l[0],{x:xs[i]-1.0,y:top,w:2.0,h:0.42,align:"center",valign:"middle",fontFace:H,fontSize:12,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
    s.addShape(p.ShapeType.line,{x:xs[i],y:top+0.42,w:0,h:bot-(top+0.42),line:{color:LINE,width:1.2,dashType:"dash"}}); });
  function msg(y,a,b,txt,c,dash){ arrow(s,xs[a],y,xs[b],y,c||"475569",dash);
    const lx=Math.min(xs[a],xs[b]); s.addText(txt,{x:lx+0.08,y:y-0.3,w:Math.abs(xs[b]-xs[a])-0.16,h:0.28,align:"center",fontFace:MONO,fontSize:9.5,color:BODY,isTextBox:true,margin:0}); }
  msg(3.15,0,1,"POST /v1/... (sk-emp)",ACC);
  msg(3.6,1,2,"Acquire(acct,4)",ACC2);
  s.addText("满则 FIFO 排队 ≤60s",{x:4.1,y:3.62,w:2.2,h:0.25,fontFace:H,fontSize:8.5,italic:true,color:FAINT,isTextBox:true,margin:0});
  msg(4.15,1,3,"model=acct-<id>/… (内部Key)",SLATEc());
  msg(4.6,3,4,"backend-api/codex (out proxy)",SLATEc());
  msg(5.05,4,1,"SSE 流",GOOD);
  msg(5.05+0.0,4,1,"",GOOD);
  // client stream + tee
  arrow(s,xs[1],5.35,xs[0],5.35,GOOD); s.addText("逐块 flush",{x:xs[0]+0.08,y:5.05,w:2.3,h:0.25,align:"center",fontFace:MONO,fontSize:9.5,color:GOOD,isTextBox:true,margin:0});
  s.addShape(p.ShapeType.line,{x:1.0,y:5.7,w:11.6,h:0,line:{color:WARN,width:1,dashType:"dash"}});
  s.addText("── 异步边界（不阻塞响应）──",{x:1.0,y:5.55,w:5,h:0.25,fontFace:H,fontSize:9,color:WARN,isTextBox:true,margin:0});
  s.addText("tee → Recorder.Submit → 解析用量/模型/TTFT → 去重落库",{x:4.05,y:5.8,w:8,h:0.3,fontFace:MONO,fontSize:10,color:PURP,isTextBox:true,margin:0});
  s.addText("defer release()（流结束或客户端断开→取消上游）",{x:4.05,y:6.1,w:8,h:0.3,fontFace:MONO,fontSize:10,color:ACC2,isTextBox:true,margin:0});
  caption(s,0.7,6.5,11.9,2,"响应用 io.TeeReader 边转发边解析：客户端拿到实时流，日志走异步、绝不拖慢首字。名额在流真正结束时才释放；客户端中途断开会取消发往上游的请求，名额不空占。");
  pageno(s,5);
  notes.push("时序关键三点：TeeReader 边转发边解析，日志异步不拖慢首字；名额流结束才释放；断开即取消上游、释放名额。");
  function SLATEc(){return "475569";} function GOODc(){return GOOD;}
})();

// ============ 6. CONCURRENCY INVARIANT ============
(()=>{ const s=slide(); kicker(s,"并发闸 · 不变量"); title(s,"名额既不泄漏，也不重复");
  // flow boxes
  node(s,0.7,2.5,2.1,0.8,"Acquire",null,ACC,"FFFFFF");
  node(s,3.4,2.35,2.5,1.1,"inflight < cap ?","fast-path 授予\nwaited=0","FFFFFF");
  node(s,3.4,3.75,2.5,0.9,"入 FIFO 队列","持 ready 通道","FFFFFF");
  node(s,6.7,3.75,2.5,1.1,"select 等待","ready | ctx.Done | timer","FFFFFF");
  node(s,9.9,2.5,2.7,0.9,"Release","持锁交接","0E9F9F","FFFFFF");
  node(s,9.9,3.65,2.7,1.1,"队列非空?","是→名额直接交队首\n否→inflight--","FFFFFF");
  arrow(s,2.8,2.9,3.4,2.9,MUTE);
  arrow(s,4.65,3.45,4.65,3.75,MUTE); s.addText("满",{x:4.7,y:3.45,w:0.5,h:0.25,fontFace:H,fontSize:9,color:BAD,isTextBox:true,margin:0});
  arrow(s,5.9,4.2,6.7,4.2,MUTE);
  arrow(s,11.25,3.4,11.25,3.65,ACC2);
  arrow(s,9.9,4.2,9.2,4.2,ACC2); s.addText("handoff",{x:9.0,y:3.9,w:1.1,h:0.25,align:"center",fontFace:MONO,fontSize:9,color:ACC2,isTextBox:true,margin:0});
  // race box
  s.addShape(p.ShapeType.roundRect,{x:6.7,y:5.2,w:5.9,h:1.15,rectRadius:0.07,fill:{color:"FEF2F2"},line:{color:BAD,width:1.2}});
  s.addText("竞态：timeout/cancel 与 handoff 同时发生",{x:6.9,y:5.3,w:5.6,h:0.3,fontFace:H,fontSize:11.5,bold:true,color:BAD,isTextBox:true,margin:0});
  s.addText("退出前持锁自检 ready：若已被授予名额 → 立刻 release 交给下一个；否则出队。二者互斥，名额守恒。",
    {x:6.9,y:5.62,w:5.6,h:0.7,fontFace:H,fontSize:10.5,color:BODY,isTextBox:true,margin:0,valign:"top"});
  code(s,0.7,5.0,5.7,1.55,[
    {t:"release := sync.Once{}  // 幂等",c:CCMT},
    {t:"granted, err := <-w.ready | ctx | timer",c:CFG},
    {t:"// 100 并发 / cap=4 压测：",c:CCMT},
    {t:"max_inflight == 4 , final == 0",c:CSTR},
  ],"internal/proxy/limiter.go  (-race)");
  caption(s,0.7,6.6,11.9,3,"不变量：任一时刻在途 ≤ cap，且请求结束后计数归零。release 用 sync.Once 幂等；超时/取消与交接的竞态在同一把锁内消解——这是整套“防降智”承诺的地基。");
  pageno(s,6);
  notes.push("并发闸的正确性靠一个不变量：名额不泄漏不重复。难点是 timeout/cancel 与 handoff 的竞态，在同一把锁内自检 ready 消解。release 幂等。压测峰值恒 4、终态 0。");
})();

// ============ 7. FINGERPRINT ============
(()=>{ const s=slide(); kicker(s,"指纹一致"); title(s,"让上游看到“同一个自洽的客户端”");
  // two paths
  node(s,0.7,2.5,2.3,0.9,"入站请求","判定 client kind","FFFFFF");
  node(s,3.7,2.15,4.0,0.85,"kind = codex","官方客户端",GOOD,"FFFFFF");
  node(s,3.7,3.2,4.0,0.85,"kind = other","第三方(Cursor/…)",WARN,"FFFFFF");
  arrow(s,3.0,2.7,3.7,2.55,MUTE); arrow(s,3.0,3.1,3.7,3.6,MUTE);
  node(s,8.4,2.15,4.2,0.85,"透传真实头","保 Version/turn-metadata\n删 X-Forwarded/Via","FFFFFF");
  node(s,8.4,3.2,4.2,0.85,"套账号设备档案","UA/版本/安装ID 固定\nSession-Id 按会话稳定","FFFFFF");
  arrow(s,7.7,2.55,8.4,2.55,GOOD); arrow(s,7.7,3.6,8.4,3.6,WARN);
  code(s,0.7,4.35,6.0,1.6,[
    {t:"SessionUUID = uuidv4fmt(",c:CFG},
    {t:"  sha256(empID | firstUserMsg))",c:CFG},
    {t:"// 同一对话恒定，跨对话不同",c:CCMT},
    {t:"egress: 每账号 proxy_url 固定，30m 自检",c:CSTR},
  ],"identity");
  s.addShape(p.ShapeType.roundRect,{x:6.9,y:4.35,w:5.7,h:1.6,rectRadius:0.07,fill:{color:"FFFBEB"},line:{color:WARN,width:1.2}});
  s.addText("为什么不伪造 TLS 指纹",{x:7.1,y:4.45,w:5.3,h:0.3,fontFace:H,fontSize:12,bold:true,color:WARN,isTextBox:true,margin:0});
  s.addText("① 每次官方发版都要重抓包，维护发散；② TLS 只是众多信号之一，账号层面(一人一号/固定IP/正常用量)更关键；③ 从“保持一致”滑向“主动伪装”，会削弱一人一真号的立场。",
    {x:7.1,y:4.78,w:5.3,h:1.1,fontFace:H,fontSize:10.5,color:BODY,isTextBox:true,margin:0,valign:"top"});
  caption(s,0.7,6.15,11.9,4,"目标是自洽而非伪装：官方客户端原样透传（去掉暴露代理的头），第三方套用该账号固定的设备档案；会话标识按对话稳定，出口 IP 每账号固定。刻意不做 TLS 伪装。");
  pageno(s,7);
  notes.push("指纹策略是“保持一致”而非“伪造”。codex 透传、other 套固定档案、会话ID按对话稳定、出口IP固定。刻意不伪造TLS，理由三条在右框。");
})();

// ============ 8. LOGGING PROBLEM (chart) ============
(()=>{ const s=slide(); kicker(s,"日志 · 问题"); title(s,"为什么不能直接存原文");
  para(s,0.7,1.7,6.0,[
    {t:"Codex 是有状态编码代理：每一轮都把整段对话历史重新发一次。",c:BODY,sz:13},
    {t:"一个长任务的第 N 轮请求体 ≈ 前 N 轮之和，累计写入是 O(N²)。",c:BODY,sz:13,b:true},
    {t:"50 人重度使用，直接存原文每天可达 5–15 GB，180 天即 TB 级——在小系统盘上必然撑爆并拖垮数据库。",c:BODY,sz:13},
    {t:"结论：存储层必须先“去冗余”，再谈放哪。",c:ACC,sz:13,b:true},
  ]);
  s.addChart(p.ChartType.bar,[{name:"每日落库(GB, 50人估算)",labels:["直接存原文","+ 片段去重","+ 前缀链增量"],values:[12.0,0.9,0.45]}],
    {x:6.9,y:2.15,w:5.7,h:3.9,barDir:"col",showTitle:true,title:"存储体量：三种策略",titleFontSize:13,titleColor:INK,titleFontFace:H,
     chartColors:[ACC], showValue:true, dataLabelPosition:"outEnd", dataLabelFontSize:11, dataLabelColor:INK, dataLabelFontFace:MONO, dataLabelFormatCode:"0.0",
     catAxisLabelColor:BODY, catAxisLabelFontSize:11, catAxisLabelFontFace:H, valAxisHidden:true, valGridLine:{style:"none"},
     showLegend:false, barGapWidthPct:60});
  caption(s,0.7,6.35,11.9,5,"估算量级示意（非实测）：片段去重把重复历史折叠成一份，前缀链只存每轮增量，合计把落库压到原始的个位数百分比。真实比例上线首周用真实数据校准。");
  pageno(s,8);
  notes.push("根因：Codex 每轮重发全历史，累计 O(N²)。直接存会到 TB 级、撑爆小盘。所以先去冗余：去重+前缀链，压到原始的几个百分点。图是量级示意，上线校准。");
})();

// ============ 9. DEDUP + CONTENT ADDRESSING ============
(()=>{ const s=slide(); kicker(s,"存储 · 机制一"); title(s,"内容寻址与去重");
  // request -> segments -> hashes -> blobs
  node(s,0.7,2.6,2.0,1.2,"请求体","instructions\ntools · input[]","FFFFFF");
  const segs=["sys","msg-1","msg-2","tool"];
  segs.forEach((t,i)=>{ node(s,3.4,2.35+i*0.72,1.5,0.6,t,null,"EEF2FF","3730A3"); });
  arrow(s,2.7,3.2,3.4,3.2,MUTE);
  s.addText("拆片段",{x:2.7,y:2.9,w:0.9,h:0.25,align:"center",fontFace:H,fontSize:9,color:MUTE,isTextBox:true,margin:0});
  segs.forEach((t,i)=>{ s.addText("sha256",{x:5.0,y:2.4+i*0.72,w:1.2,h:0.5,align:"center",valign:"middle",fontFace:MONO,fontSize:9,color:MUTE,isTextBox:true,margin:0}); arrow(s,4.9,2.65+i*0.72,6.2,2.65+i*0.72,MUTE); });
  // blobs store (dedup): show 2 requests sharing 3 of 4
  s.addShape(p.ShapeType.roundRect,{x:6.3,y:2.35,w:2.6,h:3.0,rectRadius:0.07,fill:{color:"F5F3FF"},line:{color:PURP,width:1.2}});
  s.addText("blobs（去重，按 hash）",{x:6.35,y:2.42,w:2.5,h:0.3,align:"center",fontFace:H,fontSize:10.5,bold:true,color:PURP,isTextBox:true,margin:0});
  ["#a sys","#b msg-1","#c msg-2","#d tool"].forEach((t,i)=>{ node(s,6.55,2.85+i*0.58,2.1,0.46,t,null,"FFFFFF","6D28D9"); });
  // second request reuses a,b,c
  node(s,9.4,2.6,3.1,1.2,"下一轮请求","= [sys,msg-1,msg-2, tool, msg-3]","FFFFFF");
  s.addText("↑ 前 4 段命中已存 hash → 只有 msg-3 是新字节",{x:9.4,y:3.9,w:3.1,h:0.6,fontFace:H,fontSize:10,italic:true,color:GOOD,isTextBox:true,margin:0,valign:"top"});
  arrow(s,9.4,3.2,8.9,3.2,GOOD,"dash");
  caption(s,0.7,5.6,11.9,6,"每个片段按其（脱敏后）内容的 SHA-256 寻址，zstd 压缩后写入 blobs；同一段历史被发一百次也只存一份。落库前用可配置正则对密钥/私钥/连接串脱敏——发往上游的请求绝不改动。");
  pageno(s,9);
  notes.push("内容寻址：片段按 sha256 去重，重复历史只存一份。脱敏只作用于落库副本，发往上游的请求不动。");
})();

// ============ 10. PREFIX CHAIN ============
(()=>{ const s=slide(); kicker(s,"存储 · 机制二"); title(s,"会话前缀链：每轮只存增量");
  // chain of requests
  const reqs=[["req1","[a,b]","full"],["req2","prefix_of=req1  +[c]",""],["req3","prefix_of=req2  +[d]",""]];
  const bx=[0.9,4.9,8.9], by=2.7, bw=3.1, bh=1.15;
  reqs.forEach((r,i)=>{ s.addShape(p.ShapeType.roundRect,{x:bx[i],y:by,w:bw,h:bh,rectRadius:0.07,fill:{color:CARD},line:{color:ACC,width:1.4}});
    s.addText(r[0],{x:bx[i]+0.2,y:by+0.12,w:bw-0.4,h:0.4,fontFace:MONO,fontSize:14,bold:true,color:ACC,isTextBox:true,margin:0});
    s.addText(r[1],{x:bx[i]+0.2,y:by+0.55,w:bw-0.4,h:0.5,fontFace:MONO,fontSize:12,color:BODY,isTextBox:true,margin:0}); });
  arrow(s,bx[1],by+bh/2,bx[0]+bw,by+bh/2,ACC); // req2 -> req1 (prefix pointer)
  arrow(s,bx[2],by+bh/2,bx[1]+bw,by+bh/2,ACC);
  s.addText("prefix pointer",{x:bx[0]+bw+0.02,y:by+bh/2-0.32,w:0.86,h:0.25,align:"center",fontFace:MONO,fontSize:8,color:ACC,isTextBox:true,margin:0});
  // reconstruction
  code(s,0.9,4.25,7.0,1.9,[
    {t:"Decode(req3):",c:CCMT},
    {t:"  walk prefix_of: req3→req2→req1",c:CFG},
    {t:"  hashes = req1.full[:2] + [c] + [d]",c:CSTR},
    {t:"           = [a, b, c, d]",c:CSTR},
    {t:"  每个 hash → GetBlob → 拼回完整体",c:CCMT},
  ],"reconstruct");
  s.addShape(p.ShapeType.roundRect,{x:8.1,y:4.25,w:4.5,h:1.9,rectRadius:0.07,fill:{color:"F0FDF4"},line:{color:GOOD,width:1.2}});
  s.addText("为什么有 MaxChainDepth=32",{x:8.3,y:4.35,w:4.1,h:0.3,fontFace:H,fontSize:11.5,bold:true,color:GOOD,isTextBox:true,margin:0});
  s.addText("链太长会让还原成本线性上升、且单点损坏影响后续。到 32 层强制整存一次，截断链条，用少量冗余换还原的有界成本。",
    {x:8.3,y:4.68,w:4.1,h:1.35,fontFace:H,fontSize:10.5,color:BODY,isTextBox:true,margin:0,valign:"top"});
  caption(s,0.7,6.35,11.9,7,"requests 只保存有序哈希列表；当本轮以上一轮为严格前缀时，仅存尾部增量 + 指向上一轮的指针（会话行上另存完整列表供快速比对）。还原沿链回溯。");
  pageno(s,10);
  notes.push("前缀链：本轮以上轮为前缀时只存增量+指针，还原沿链回溯。MaxChainDepth=32 截断，用少量冗余换有界的还原成本、限制单点损坏传播。");
})();

// ============ 11. BLOB BACKEND (the change) ============
(()=>{ const s=slide();
  s.addShape(p.ShapeType.roundRect,{x:0.7,y:0.46,w:1.7,h:0.4,rectRadius:0.07,fill:{color:WARN}});
  s.addText("本次技术改动",{x:0.7,y:0.46,w:1.7,h:0.4,align:"center",valign:"middle",fontFace:H,fontSize:12,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
  title(s,"可插拔 Blob 后端：字节走桶，元数据留库");
  // inline
  s.addText("inline (postgres, 默认)",{x:0.9,y:2.35,w:5.5,h:0.3,fontFace:H,fontSize:12,bold:true,color:BODY,isTextBox:true,margin:0});
  node(s,0.9,2.75,1.9,0.7,"PutBlobs",null,ACC,"FFFFFF");
  node(s,3.3,2.72,3.0,0.76,"blobs 行","data_zstd = 压缩字节","1E293B","FFFFFF");
  arrow(s,2.8,3.1,3.3,3.1,MUTE);
  // external
  s.addText("external (s3 / r2 / oss)",{x:0.9,y:3.95,w:5.5,h:0.3,fontFace:H,fontSize:12,bold:true,color:GOOD,isTextBox:true,margin:0});
  node(s,0.9,4.5,1.9,0.7,"PutBlobs",null,ACC,"FFFFFF");
  node(s,3.3,4.3,3.0,0.72,"blobs 行","data_zstd = NULL（仅元数据）","1E293B","FFFFFF");
  node(s,3.3,5.12,3.0,0.72,"对象存储 桶","key=blobs/<hex> ← 压缩字节",PURP,"FFFFFF");
  arrow(s,2.8,4.66,3.3,4.66,MUTE); arrow(s,2.85,4.9,3.3,5.4,PURP);
  code(s,6.7,2.75,5.9,2.65,[
    {t:"// 去重不丢：只对“新插入”的行上传字节",c:CCMT},
    {t:"INSERT ... ON CONFLICT DO UPDATE last_ref",c:CFG},
    {t:"  RETURNING (xmax = 0) AS inserted",c:CKEY},
    {t:"if inserted { backend.Put(key, zstd) }",c:CSTR},
    {t:"",},
    {t:"GetBlob:  行内有字节→PG，否则→桶",c:CFG},
    {t:"Purge:    先删桶对象，再删 PG 行",c:CFG},
    {t:"新旧行共存 → 平滑迁移，无需回填",c:CSTR},
  ],"internal/store/blobs.go");
  para(s,6.7,5.6,5.9,[{t:"元数据、去重、检索、GC 全部不变；只有“字节存哪”变了。一个 env 开关切换，migration 仅把 data_zstd 放开为可空。",c:BODY,sz:11.5}]);
  caption(s,0.7,6.35,11.9,8,"动机不只是每 GB 更便宜（R2≈$0.015 vs SSD≈$0.10，且出流量免费），更是把“不断增长的原文”从昂贵的小系统盘解耦，让小配置机器长期稳定运行；桶侧生命周期规则给 180 天过期兜底。");
  pageno(s,11);
  notes.push("本次改动：字节走对象存储、元数据留库。用 xmax=0 只对新片段上传，去重写节省不丢。GetBlob/Purge 相应分支，新旧共存平滑迁移。价值是把增长的原文从系统盘解耦。");
})();

// ============ 12. FAILURE MODES ============
(()=>{ const s=slide(); kicker(s,"韧性"); title(s,"故障下保持的不变量");
  const rows=[
    ["数据库暂时不可用","Recorder 落 spool 文件，恢复后回放","日志至少一次，不丢",GOOD],
    ["客户端中途断开","ctx 取消上游请求 + 释放并发名额","名额不空占，不计为错误",ACC2],
    ["上游 429 / 限额","置账号 rate_limited + 记恢复时间","后续请求快速失败，不硬打",WARN],
    ["上游 401 / 撤销","置 needs_login + 飞书告警","坏账号被隔离，其余不受影响",WARN],
    ["对象存储删除部分失败","先删对象后删行，失败留行下轮重试","不产生悬空引用",ACC],
    ["响应模型 ≠ 请求模型","记 model_mismatch 信号 → 降智看板","被降智账号可见、可处置",PURP],
    ["飞书离职同步","DisableEmployeesNotIn → Key 失效","离职即失权",BAD],
  ];
  let y=2.4; const x=0.7,w=11.9,hh=0.58;
  rows.forEach(r=>{ s.addShape(p.ShapeType.roundRect,{x,y,w,h:hh,rectRadius:0.05,fill:{color:CARD},line:{color:LINE,width:1}});
    s.addShape(p.ShapeType.ellipse,{x:x+0.22,y:y+hh/2-0.05,w:0.1,h:0.1,fill:{color:r[3]}});
    s.addText(r[0],{x:x+0.5,y,w:3.3,h:hh,fontFace:H,fontSize:12,bold:true,color:INK,isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[1],{x:x+3.9,y,w:5.0,h:hh,fontFace:H,fontSize:11,color:BODY,isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[2],{x:x+9.0,y,w:w-9.2,h:hh,fontFace:H,fontSize:10.5,italic:true,color:r[3],isTextBox:true,margin:0,valign:"middle"});
    y+=hh+0.1; });
  pageno(s,12);
  notes.push("系统的价值在故障下才显现。每种故障对应一个被保持的不变量：日志至少一次、名额不空占、坏账号隔离、无悬空引用、降智可见、离职即失权。");
})();

// ============ 13. DATA MODEL ============
(()=>{ const s=slide(); kicker(s,"数据模型"); title(s,"关键表与非显然约束");
  function tbl(x,y,w,name,rows,accent){ const h=0.5+rows.length*0.32+0.12;
    s.addShape(p.ShapeType.roundRect,{x,y,w,h,rectRadius:0.06,fill:{color:CARD},line:{color:LINE,width:1},shadow:sh2()});
    s.addShape(p.ShapeType.roundRect,{x,y,w,h:0.44,rectRadius:0.06,fill:{color:accent}});
    s.addText(name,{x:x+0.15,y,w:w-0.3,h:0.44,valign:"middle",fontFace:MONO,fontSize:12,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
    rows.forEach((r,i)=>{ s.addText([{text:r[0],options:{fontFace:MONO,fontSize:10,color:INK,breakLine:false}},{text:"  "+r[1],options:{fontFace:H,fontSize:9,color:MUTE,breakLine:true}}],
      {x:x+0.18,y:y+0.5+i*0.32,w:w-0.36,h:0.3,isTextBox:true,margin:0,valign:"middle"}); });
    return h; }
  function sh2(){ return {type:"outer",color:"9AA6D6",opacity:0.22,blur:7,offset:2,angle:90}; }
  tbl(0.7,2.3,3.8,"employees",[["feishu_open_id","登录认人"],["is_super_admin","超管"],["notice_ack_at","告知留痕"]],ACC);
  tbl(4.75,2.3,3.9,"upstream_accounts",[["employee_id UNIQUE","1:1 绑定"],["device_profile","设备档案"],["ip_group_id","出口分组"]],ACC2);
  tbl(8.9,2.3,3.7,"api_keys",[["key_hash","仅存哈希"],["部分唯一索引","一员工一活跃 Key"]],PURP);
  tbl(0.7,4.35,3.8,"requests",[["req_prefix_of","前缀链指针"],["req_hashes","增量哈希"],["tokens_*/ttft","用量"]],ACC);
  tbl(4.75,4.35,3.9,"blobs",[["hash PK","内容寻址"],["data_zstd 可空","NULL=外部后端"],["last_referenced_at","GC 依据"]],PURP);
  tbl(8.9,4.35,3.7,"audit_logs",[["触发器","禁 UPDATE/DELETE"],["append-only","查看原文留痕"]],BAD);
  caption(s,0.7,6.35,11.9,9,"非显然处：一员工一活跃 Key 用“部分唯一索引(WHERE revoked_at IS NULL)”在库层保证；审计表用触发器强制只追加；blobs.data_zstd 可空是区分内/外部存储的标记，也让两种行安全共存。");
  pageno(s,13);
  notes.push("数据模型三个非显然约束：部分唯一索引保证一员工一活跃Key；触发器让审计只追加；data_zstd可空区分内外部存储。");
})();

// ============ 14. VALIDATION ============
(()=>{ const s=slide(); kicker(s,"验证"); title(s,"什么已证明，什么需要真号");
  // pyramid-ish
  const rows=[
    ["单元 / 竞态","go test -race 全绿；限流器/编码/脱敏/SSE 解析表驱动覆盖","已证明",GOOD],
    ["集成","假 CPA 上游：前缀路由、峰值并发=4、错误映射、限额反应、流式透传","已证明",GOOD],
    ["Blob 后端","in-memory 假后端：外部路径 / 去重 / 清理","已证明",GOOD],
    ["端到端","serve 二进制 + 浏览器：登录→发Key→限并发→日志→看板","已证明",GOOD],
    ["真实链路","OAuth 登录 / 真实额度头 / token 刷新走代理 / 降智观察","需预发+真号",WARN],
  ];
  let y=2.5; const x=0.7,w=11.9,hh=0.72;
  rows.forEach(r=>{ s.addShape(p.ShapeType.roundRect,{x,y,w,h:hh,rectRadius:0.06,fill:{color:CARD},line:{color:LINE,width:1}});
    s.addShape(p.ShapeType.roundRect,{x,y,w:0.14,h:hh,rectRadius:0.04,fill:{color:r[3]}});
    s.addText(r[0],{x:x+0.4,y,w:2.3,h:hh,fontFace:H,fontSize:13.5,bold:true,color:INK,isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[1],{x:x+2.8,y,w:7.0,h:hh,fontFace:H,fontSize:11,color:BODY,isTextBox:true,margin:0,valign:"middle"});
    s.addShape(p.ShapeType.roundRect,{x:x+w-2.05,y:y+hh/2-0.18,w:1.85,h:0.36,rectRadius:0.06,fill:{color:r[3]}});
    s.addText(r[2],{x:x+w-2.05,y:y+hh/2-0.18,w:1.85,h:0.36,align:"center",valign:"middle",fontFace:H,fontSize:10.5,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
    y+=hh+0.12; });
  caption(s,0.7,6.45,11.9,10,"我们区分“已在代码里证明的”和“只能在真实环境证明的”。后者（真实账号存活、指纹是否被上游接受）列成上线前清单，用一个可弃号在干净美国 IP 上观察数日，作为放量的 go/no-go。");
  pageno(s,14);
  notes.push("诚实区分已证明(竞态/集成/blob后端/端到端)和需真号(OAuth/额度/刷新走代理/降智)。后者是上线前清单，用可弃号+干净美国IP观察数天再放量。");
})();

// ============ 15. NON-GOALS / RATIONALE ============
(()=>{ const s=slide(true);
  s.addText("我们刻意没做什么",{x:0.7,y:1.4,w:11,h:0.7,fontFace:H,fontSize:30,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
  const rows=[
    ["不接 Claude Code 订阅","Anthropic 明令禁止且主动封号——比 OpenAI 风险更高，第一期直接排除"],
    ["不伪造 TLS 指纹","保持一致而非伪装；维护成本高且削弱一人一真号的立场"],
    ["requests 暂不做月分区","50 人一年约 2000 万行，B-tree 足够；过早分区徒增复杂度，超 5000 万行再迁"],
    ["员工自带订阅暂不做","已在表结构预留 owner_type，第一期聚焦核心链路"],
    ["blobs 验证期先用 Postgres","小体量最简单；后端已抽象，上生产一个开关切 R2"],
  ];
  let y=2.5;
  rows.forEach(r=>{ s.addText("—",{x:0.7,y,w:0.4,h:0.5,fontFace:H,fontSize:16,bold:true,color:"818CF8",isTextBox:true,margin:0});
    s.addText(r[0],{x:1.2,y,w:4.4,h:0.7,fontFace:H,fontSize:14,bold:true,color:"FFFFFF",isTextBox:true,margin:0,valign:"top"});
    s.addText(r[1],{x:5.7,y,w:6.9,h:0.7,fontFace:H,fontSize:11.5,color:"CBD5E1",isTextBox:true,margin:0,valign:"top"});
    y+=0.82; });
  s.addText("YAGNI 是一种设计纪律：把复杂度花在真正的约束上，其余留到证据出现。",{x:0.7,y:6.7,w:11.5,h:0.4,fontFace:H,fontSize:12.5,italic:true,color:"64748B",isTextBox:true,margin:0});
  pageno(s,15);
  notes.push("刻意的非目标同样是设计的一部分：不接Claude订阅、不伪造TLS、暂不月分区、暂不做自带订阅、验证期blob用PG。把复杂度花在真约束上。");
})();

p.slides.forEach((sl,i)=>{ if(notes[i]) sl.addNotes(notes[i]); });
const out = process.argv[2] || "deep.pptx";
p.writeFile({fileName:out}).then(f=>console.log("wrote",f));
