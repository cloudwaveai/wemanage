const pptxgen = require("pptxgenjs");
const p = new pptxgen();
p.layout = "LAYOUT_WIDE"; // 13.3 x 7.5
const W = 13.333, H = 7.5;

// Midnight Executive palette
const NAVY = "1E2761", INK = "16204E", ICE = "CADCFC", WHITE = "FFFFFF";
const SLATE = "3A4374", MUTE = "6B7299", LIGHT = "F4F7FF", CARD = "FFFFFF";
const ACCENT = "3B6FE0", GOOD = "1E9E6A", WARN = "D98A00", BAD = "C0392B", PURP = "7A5CC0";
const HFONT = process.env.DECK_FONT || "微软雅黑", BFONT = process.env.DECK_FONT || "微软雅黑";

const notes = [];
function bgDark(s){ s.background = { color: NAVY }; }
function bgLight(s){ s.background = { color: LIGHT }; }

function kicker(s, t, color){
  s.addText(t, { x:0.9, y:0.62, w:8, h:0.4, fontFace:BFONT, fontSize:13, color:color||ACCENT, bold:true, charSpacing:2, isTextBox:true, margin:0 });
}
function title(s, t, color){
  s.addText(t, { x:0.9, y:0.95, w:11.5, h:1.0, fontFace:HFONT, fontSize:34, bold:true, color:color||INK, isTextBox:true, margin:0 });
}
function pageNo(s, n){
  s.addText(String(n).padStart(2,"0"), { x:12.4, y:6.95, w:0.7, h:0.35, align:"right", fontFace:BFONT, fontSize:11, color:MUTE, isTextBox:true, margin:0 });
}
function shadow(){ return { type:"outer", color:"9AA6D6", opacity:0.35, blur:10, offset:3, angle:90 }; }
function card(s, x, y, w, h, fill){
  s.addShape(p.ShapeType.roundRect, { x, y, w, h, rectRadius:0.10, fill:{ color: fill||CARD }, line:{ color:"E3E9F7", width:1 }, shadow:shadow() });
}
function numCircle(s, x, y, d, label, fill){
  s.addShape(p.ShapeType.ellipse, { x, y, w:d, h:d, fill:{ color: fill||ACCENT } });
  s.addText(label, { x, y, w:d, h:d, align:"center", valign:"middle", fontFace:HFONT, fontSize: d>0.75?22:16, bold:true, color:WHITE, isTextBox:true, margin:0 });
}

// ---------- Slide 1: Title ----------
(() => {
  const s = p.addSlide(); bgDark(s);
  s.addShape(p.ShapeType.ellipse, { x:9.7, y:-2.2, w:6.5, h:6.5, fill:{ color:INK } });
  s.addShape(p.ShapeType.ellipse, { x:11.2, y:3.6, w:4.8, h:4.8, fill:{ color:"25317A" } });
  s.addText("方案汇报", { x:0.9, y:1.5, w:6, h:0.5, fontFace:BFONT, fontSize:15, color:ICE, bold:true, charSpacing:3, isTextBox:true, margin:0 });
  s.addText("AI 订阅统一分发网关", { x:0.9, y:2.05, w:11.2, h:1.5, fontFace:HFONT, fontSize:52, bold:true, color:WHITE, isTextBox:true, margin:0 });
  s.addText("一人一号 · 全量留痕 · 稳定合规", { x:0.9, y:3.55, w:11, h:0.7, fontFace:HFONT, fontSize:24, color:ICE, isTextBox:true, margin:0 });
  s.addText("给全体员工统一分发 AI 订阅，同时把用量、成本与合规牢牢握在公司手里。",
    { x:0.9, y:4.5, w:9.6, h:0.7, fontFace:BFONT, fontSize:15, color:"AEBBEC", isTextBox:true, margin:0 });
  s.addText("云涌世纪  ·  2026-09", { x:0.9, y:6.6, w:6, h:0.4, fontFace:BFONT, fontSize:13, color:"8E9AD0", isTextBox:true, margin:0 });
  notes.push("开场：我们要解决的问题是——给员工配 AI 订阅，但不能直接发账号、直接报销，否则用量、成本、合规都失控。这套网关就是答案。");
})();

// ---------- Slide 2: 痛点 ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "背景与痛点"); title(s, "直接给员工发 AI 账号，等于把管理权交出去");
  const items = [
    ["用量看不见", "谁在用、用了多少、用来做什么，公司完全不掌握。", BAD],
    ["账号易被风控", "多设备、异常并发容易触发平台风控，甚至被“降智”。", WARN],
    ["成本难归集", "报销口径混乱，无法按人、按项目核算与预测。", ACCENT],
    ["合规无抓手", "对话内容散落各处，既不留痕，也难满足告知与留存要求。", PURP],
  ];
  const cw=5.6, ch=1.95, gx=0.9, gy=2.15, sx=0.55, sy=0.35;
  items.forEach((it,i)=>{
    const col=i%2, row=Math.floor(i/2);
    const x=gx+col*(cw+sx), y=gy+row*(ch+sy);
    card(s,x,y,cw,ch);
    s.addShape(p.ShapeType.roundRect,{x:x+0.35,y:y+0.35,w:0.16,h:ch-0.7,rectRadius:0.08,fill:{color:it[2]}});
    s.addText(it[0], { x:x+0.75, y:y+0.32, w:cw-1.1, h:0.5, fontFace:HFONT, fontSize:21, bold:true, color:INK, isTextBox:true, margin:0 });
    s.addText(it[1], { x:x+0.75, y:y+0.9, w:cw-1.1, h:0.9, fontFace:BFONT, fontSize:14.5, color:SLATE, isTextBox:true, margin:0, valign:"top" });
  });
  pageNo(s,2);
  notes.push("四个痛点：用量看不见、账号易被风控、成本难归集、合规无抓手。这四条正是我们设计目标的来源。");
})();

// ---------- Slide 3: 目标 ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "设计目标"); title(s, "五个硬指标，一个都不妥协");
  const goals = [
    ["1","一人一号","员工的 Key 固定路由到本人专属账号，绝不做号池轮询。"],
    ["2","全量日志","请求与回复原文、Token、耗时全部留在公司，可查可分析。"],
    ["3","限并发 ≤ 4","每个账号同时在途请求封顶，降低风控与降智风险。"],
    ["4","管理员管控","账号只能由管理员添加与绑定，员工无法自行修改。"],
    ["5","指纹一致","请求头、会话标识、出口 IP 稳定自洽，像同一个官方客户端。"],
  ];
  const cw=3.72, ch=2.35, gx=0.9, sx=0.36, gy=2.25;
  goals.slice(0,3).forEach((g,i)=>{ drawGoal(s, gx+i*(cw+sx), gy, cw, ch, g); });
  const gy2=gy+ch+0.35, cw2=5.66, gx2=0.9, sx2=0.4;
  goals.slice(3).forEach((g,i)=>{ drawGoal(s, gx2+i*(cw2+sx2), gy2, cw2, ch, g); });
  pageNo(s,3);
  function drawGoal(s,x,y,w,h,g){
    card(s,x,y,w,h);
    numCircle(s, x+0.35, y+0.32, 0.85, g[0], ACCENT);
    s.addText(g[1], { x:x+0.35, y:y+1.3, w:w-0.7, h:0.5, fontFace:HFONT, fontSize:20, bold:true, color:INK, isTextBox:true, margin:0 });
    s.addText(g[2], { x:x+0.35, y:y+1.82, w:w-0.7, h:0.9, fontFace:BFONT, fontSize:13.5, color:SLATE, isTextBox:true, margin:0, valign:"top" });
  }
  notes.push("五个目标：一人一号、全量日志、限并发4、管理员管控、指纹一致。接下来讲怎么实现。");
})();

// ---------- Slide 4: 架构 ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "总体架构"); title(s, "自研网关在前，成熟开源 CPA 在后");
  const y=2.8, bh=1.55;
  const boxes = [
    ["员工客户端", "Codex / Cursor\nCherry Studio", "8A93C0"],
    ["网关（自研）", "身份 · 并发 · 指纹\n日志 · 看板 · 日报", ACCENT],
    ["CLIProxyAPI", "OAuth 刷新\n上游协议对接", "2E8B8B"],
    ["ChatGPT 上游", "分组固定出口 IP\n每号一个代理", NAVY],
  ];
  const bw=2.55, gap=0.5, startx=0.95;
  boxes.forEach((b,i)=>{
    const x=startx+i*(bw+gap);
    s.addShape(p.ShapeType.roundRect,{x,y,w:bw,h:bh,rectRadius:0.10,fill:{color:b[2]},shadow:shadow()});
    s.addText(b[0],{x,y:y+0.22,w:bw,h:0.5,align:"center",fontFace:HFONT,fontSize:18,bold:true,color:WHITE,isTextBox:true,margin:0});
    s.addText(b[1],{x,y:y+0.75,w:bw,h:0.7,align:"center",fontFace:BFONT,fontSize:12.5,color:"EAF0FF",isTextBox:true,margin:0,valign:"top"});
    if(i<boxes.length-1){
      s.addShape(p.ShapeType.rightArrow,{x:x+bw+0.08,y:y+bh/2-0.16,w:gap-0.16,h:0.32,fill:{color:MUTE}});
    }
  });
  s.addText("员工只拿到公司签发的内部 Key 与地址；网关承载全部公司逻辑，CPA 保持原版、跟随上游升级，是全系统里最易随协议变化的部分。",
    { x:0.95, y:4.9, w:11.4, h:0.9, fontFace:BFONT, fontSize:15, color:SLATE, isTextBox:true, margin:0, valign:"top" });
  s.addText("全部以 Docker Compose 部署在一台海外服务器，CPA 不对外暴露端口。",
    { x:0.95, y:5.7, w:11.4, h:0.5, fontFace:BFONT, fontSize:13.5, italic:true, color:MUTE, isTextBox:true, margin:0 });
  pageNo(s,4);
  notes.push("架构：员工→网关→CPA→ChatGPT。网关是我们写的、承载公司逻辑；CPA 是成熟开源项目，保持原版跟随升级，换镜像即可。");
})();

// ---------- Slide 5: 一人一号 & 并发 ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "核心机制 ①"); title(s, "一人一号 · 每账号并发封顶 4");
  card(s, 0.9, 2.2, 5.7, 4.2);
  s.addText("一人一号，真隔离", { x:1.25, y:2.45, w:5, h:0.5, fontFace:HFONT, fontSize:20, bold:true, color:ACCENT, isTextBox:true, margin:0 });
  const l1 = [
    "每个账号带唯一前缀 acct-<ID>",
    "网关把模型名改写成 acct-<ID>/<模型> 再转发",
    "开启 force-model-prefix：该前缀只命中这一个账号",
    "账号失效则请求直接失败，绝不改走他人账号",
  ];
  bullets(s, 1.25, 3.05, 5.1, l1, ACCENT);
  card(s, 6.9, 2.2, 5.5, 4.2);
  s.addText("并发闸：FIFO 排队", { x:7.25, y:2.45, w:5, h:0.5, fontFace:HFONT, fontSize:20, bold:true, color:GOOD, isTextBox:true, margin:0 });
  const l2 = [
    "每账号内存信号量，默认上限 4，可单独调",
    "第 5 个请求排队，最长等 60 秒（可配）",
    "客户端断开即取消上游请求，名额不空占",
    "超时返回 429，Codex 自行退避重试",
  ];
  bullets(s, 7.25, 3.05, 4.9, l2, GOOD);
  s.addText("已用假上游压测验证：20 并发打同一账号，上游观测到的峰值并发恒为 4。",
    { x:7.25, y:5.55, w:4.9, h:0.7, fontFace:BFONT, fontSize:13, italic:true, color:MUTE, isTextBox:true, margin:0, valign:"top" });
  pageNo(s,5);
  notes.push("一人一号靠前缀路由+force-model-prefix做真隔离；并发靠每账号FIFO信号量封顶4，已压测验证峰值并发恒为4。");
})();

// ---------- Slide 6: 日志 & 分析 ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "核心机制 ②"); title(s, "全量留痕，却不撑爆磁盘");
  // stat callouts
  const stats = [["按片段去重","原始体量的几个百分点"],["180 天","原文自动清理，统计永久保留"],["pg_trgm","中文全文检索"]];
  const cw=3.72, gx=0.9, sx=0.36, y=2.2, h=1.55;
  stats.forEach((st,i)=>{
    const x=gx+i*(cw+sx); card(s,x,y,cw,h);
    s.addText(st[0],{x:x+0.3,y:y+0.22,w:cw-0.6,h:0.6,fontFace:HFONT,fontSize:24,bold:true,color:ACCENT,isTextBox:true,margin:0});
    s.addText(st[1],{x:x+0.3,y:y+0.9,w:cw-0.6,h:0.5,fontFace:BFONT,fontSize:13,color:SLATE,isTextBox:true,margin:0});
  });
  card(s, 0.9, 4.0, 11.5, 2.5);
  s.addText("从留痕到洞察", { x:1.25, y:4.2, w:6, h:0.5, fontFace:HFONT, fontSize:20, bold:true, color:INK, isTextBox:true, margin:0 });
  const cols = [
    ["用量看板","按人、按模型、按客户端统计 Token 与请求量"],
    ["原文可查","还原完整对话，按人、时间、关键词检索"],
    ["AI 工作日报","每天自动汇总每人做了什么、卡在哪，推送飞书"],
  ];
  cols.forEach((c,i)=>{
    const x=1.25+i*3.75;
    numCircle(s, x, 4.85, 0.5, "•", ACCENT);
    s.addText(c[0],{x:x+0.65,y:4.8,w:3.0,h:0.4,fontFace:HFONT,fontSize:15.5,bold:true,color:INK,isTextBox:true,margin:0});
    s.addText(c[1],{x:x+0.65,y:5.2,w:3.05,h:1.0,fontFace:BFONT,fontSize:12.5,color:SLATE,isTextBox:true,margin:0,valign:"top"});
  });
  s.addText("日报由独立的小模型生成，数字全部来自数据库、绝不让模型编造；不占用员工订阅。",
    { x:1.25, y:6.05, w:11, h:0.4, fontFace:BFONT, fontSize:12.5, italic:true, color:MUTE, isTextBox:true, margin:0 });
  pageNo(s,6);
  notes.push("Codex每轮重发全部历史，直接存会到TB级；我们按内容片段去重，只占几个百分点。原文可查、有看板、有AI日报，数字来自SQL不由模型生成。");
})();

// ---------- Slide 7: 指纹 & 出口IP ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "核心机制 ③"); title(s, "指纹一致，账号更稳");
  const rows = [
    ["官方 Codex 客户端","透传真实请求头，只剥离会暴露代理的字段（X-Forwarded 等）", GOOD],
    ["其他第三方客户端","套用账号固定的“设备档案”：UA、版本、安装 ID、稳定会话 ID", ACCENT],
    ["出口 IP","分组固定，每号绑定一个静态代理，永不漂移；每 30 分钟自检", PURP],
    ["降智监测","响应模型与请求不符、推理量被封顶 → 标红告警", WARN],
  ];
  let y=2.25; const h=0.98, x=0.9, w=11.5;
  rows.forEach((r)=>{
    card(s,x,y,w,h);
    s.addShape(p.ShapeType.roundRect,{x:x+0.3,y:y+0.24,w:0.16,h:h-0.48,rectRadius:0.08,fill:{color:r[2]}});
    s.addText(r[0],{x:x+0.65,y:y+0.12,w:3.3,h:h-0.24,fontFace:HFONT,fontSize:16.5,bold:true,color:INK,isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[1],{x:x+4.0,y:y+0.12,w:w-4.3,h:h-0.24,fontFace:BFONT,fontSize:14,color:SLATE,isTextBox:true,margin:0,valign:"middle"});
    y+=h+0.22;
  });
  pageNo(s,7);
  notes.push("指纹：官方客户端透传，第三方套设备档案；出口IP分组固定不漂移并自检；对降智做监测告警。我们保持一致而不是伪造TLS，理由在合规页展开。");
})();

// ---------- Slide 8: 管理与权限 ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "管理与权限"); title(s, "飞书登录，三级可见，操作留痕");
  const cols = [
    ["超级管理员","查看全部数据与审计\n添加/绑定账号、改设置", NAVY],
    ["部门负责人","仅查看本部门及下级员工\n用量、原文、日报（只读）", ACCENT],
    ["员工","仅查看自己的 Key\n用量、额度、日报", "2E8B8B"],
  ];
  const cw=3.72, gx=0.9, sx=0.36, y=2.25, h=2.6;
  cols.forEach((c,i)=>{
    const x=gx+i*(cw+sx);
    s.addShape(p.ShapeType.roundRect,{x,y,w:cw,h:h,rectRadius:0.10,fill:{color:c[2]},shadow:shadow()});
    s.addText(c[0],{x:x+0.3,y:y+0.3,w:cw-0.6,h:0.5,fontFace:HFONT,fontSize:19,bold:true,color:WHITE,isTextBox:true,margin:0});
    s.addText(c[1],{x:x+0.3,y:y+1.0,w:cw-0.6,h:1.4,fontFace:BFONT,fontSize:14,color:"EAF0FF",isTextBox:true,margin:0,valign:"top"});
  });
  card(s, 0.9, 5.15, 11.5, 1.35);
  s.addText("凡是查看他人原文、绑定/解绑、重置 Key、改设置，全部写入不可篡改的审计表；员工首次登录必须确认“使用告知”后才能拿到 Key。",
    { x:1.25, y:5.35, w:10.9, h:1.0, fontFace:BFONT, fontSize:14.5, color:SLATE, isTextBox:true, margin:0, valign:"top" });
  pageNo(s,8);
  notes.push("三级权限按飞书组织自动划分；所有敏感操作留审计；员工先确认告知再拿Key。");
})();

// ---------- Slide 9: 合规 ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "合规立场", WARN); title(s, "把话说在前面：我们如何守住合规");
  const items = [
    ["一人一号，不是共享","严格一人一个付费账号，不做号池、不转售——这与平台禁止的“多人共享”本质不同。", GOOD],
    ["正视灰色地带","“订阅经网关以 API 使用”仍属灰色地带；我们用固定 IP、限并发、健康监测把风险降到最低，并接受剩余风险。", WARN],
    ["不接入 Claude 订阅","Anthropic 明确禁止第三方中转其订阅凭证，故第一期不接；需要 Claude 走官方 API 或 Team。", BAD],
    ["依法告知与留存","按《个人信息保护法》事先告知并取得同意，原文脱敏、限权查看、180 天清理。", ACCENT],
  ];
  const cw=5.6, ch=1.95, gx=0.9, sx=0.55, gy=2.2, sy=0.32;
  items.forEach((it,i)=>{
    const col=i%2,row=Math.floor(i/2),x=gx+col*(cw+sx),y=gy+row*(ch+sy);
    card(s,x,y,cw,ch);
    s.addText(it[0],{x:x+0.35,y:y+0.28,w:cw-0.7,h:0.5,fontFace:HFONT,fontSize:17.5,bold:true,color:it[2],isTextBox:true,margin:0});
    s.addText(it[1],{x:x+0.35,y:y+0.82,w:cw-0.7,h:1.0,fontFace:BFONT,fontSize:13,color:SLATE,isTextBox:true,margin:0,valign:"top"});
  });
  pageNo(s,9);
  notes.push("合规是重点：一人一号非共享；坦白订阅转API是灰色地带、我们把风险降到最低；不接Claude订阅；按PIPL告知同意、脱敏、限权、180天清理。");
})();

// ---------- Slide 10: 交付 ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "交付与工程质量"); title(s, "分四期交付，测试全绿");
  // timeline
  const steps=[["M1","数据面跑通","一人一号·并发·指纹·去重落库"],["M2","身份与后台","飞书登录·自助页·管理后台"],["M3","看板与健康","用量·检索·降智·出口IP告警"],["M4","日报与清理","AI日报·180天清理"]];
  const y=2.35, cw=2.6, gx=0.95, sx=0.44;
  steps.forEach((st,i)=>{
    const x=gx+i*(cw+sx);
    numCircle(s, x+cw/2-0.35, y, 0.7, st[0], ACCENT);
    if(i<steps.length-1) s.addShape(p.ShapeType.line,{x:x+cw/2+0.4,y:y+0.35,w:cw+sx-0.8,h:0,line:{color:"C3CCEC",width:2,dashType:"dash"}});
    card(s,x,y+0.95,cw,1.6);
    s.addText(st[1],{x:x+0.2,y:y+1.12,w:cw-0.4,h:0.5,align:"center",fontFace:HFONT,fontSize:15,bold:true,color:INK,isTextBox:true,margin:0});
    s.addText(st[2],{x:x+0.2,y:y+1.6,w:cw-0.4,h:0.85,align:"center",fontFace:BFONT,fontSize:11.5,color:SLATE,isTextBox:true,margin:0,valign:"top"});
  });
  card(s, 0.95, 5.35, 11.45, 1.2, INK);
  s.addText("Go 1.26 · PostgreSQL 16 · React · Caddy · Docker Compose",
    { x:1.3, y:5.55, w:11, h:0.4, fontFace:HFONT, fontSize:15, bold:true, color:WHITE, isTextBox:true, margin:0 });
  s.addText("全部模块 go test -race 通过；数据面用假上游做集成测试；上线前另有真实账号验证清单。",
    { x:1.3, y:5.98, w:11, h:0.4, fontFace:BFONT, fontSize:13, color:ICE, isTextBox:true, margin:0 });
  pageNo(s,10);
  notes.push("四期交付：M1数据面、M2身份后台、M3看板健康、M4日报清理。工程上全部竞态测试通过，上线前还有真实账号验证清单。");
})();

// ---------- Slide 11: 收益 ----------
(() => {
  const s = p.addSlide(); bgLight(s);
  kicker(s, "为什么值得做"); title(s, "一套网关，四重收益");
  const items=[["成本可控","按人、按项目核算与预测，告别糊涂账"],["用量透明","谁在用、用得怎样，一屏看清"],["账号更稳","限并发+固定IP+降智监测，少踩风控"],["工作洞察","AI 日报把使用数据变成管理信息"]];
  const cw=5.6, ch=1.9, gx=0.9, sx=0.55, gy=2.2, sy=0.35;
  items.forEach((it,i)=>{
    const col=i%2,row=Math.floor(i/2),x=gx+col*(cw+sx),y=gy+row*(ch+sy);
    card(s,x,y,cw,ch);
    numCircle(s, x+0.35, y+0.4, 0.9, String(i+1), [GOOD,ACCENT,PURP,WARN][i]);
    s.addText(it[0],{x:x+1.5,y:y+0.35,w:cw-1.8,h:0.5,fontFace:HFONT,fontSize:19,bold:true,color:INK,isTextBox:true,margin:0});
    s.addText(it[1],{x:x+1.5,y:y+0.9,w:cw-1.8,h:0.8,fontFace:BFONT,fontSize:13.5,color:SLATE,isTextBox:true,margin:0,valign:"top"});
  });
  pageNo(s,11);
  notes.push("四重收益：成本可控、用量透明、账号更稳、工作洞察。");
})();

// ---------- Slide 12: 结束 ----------
(() => {
  const s = p.addSlide(); bgDark(s);
  s.addShape(p.ShapeType.ellipse, { x:-2.0, y:3.8, w:6.5, h:6.5, fill:{ color:INK } });
  s.addText("下一步", { x:0.9, y:1.7, w:6, h:0.5, fontFace:BFONT, fontSize:15, color:ICE, bold:true, charSpacing:3, isTextBox:true, margin:0 });
  s.addText("先小范围试点，跑通验证清单后放量", { x:0.9, y:2.25, w:11.4, h:1.2, fontFace:HFONT, fontSize:34, bold:true, color:WHITE, isTextBox:true, margin:0 });
  const steps=["准备海外服务器与分组静态出口 IP","接入飞书自建应用，同步通讯录","管理员逐个添加账号并绑定员工","完成上线前验证清单，再逐步放量"];
  bullets(s, 0.95, 3.9, 8.6, steps, ICE, WHITE);
  s.addText("Q & A", { x:0.9, y:6.5, w:4, h:0.6, fontFace:HFONT, fontSize:26, bold:true, color:ICE, isTextBox:true, margin:0 });
  notes.push("下一步：备服务器与IP、接飞书、逐个加账号绑人、跑完验证清单再放量。谢谢，进入Q&A。");
})();

function bullets(s, x, y, w, arr, dot, textColor){
  const items = arr.map((t,i)=>({ text:t, options:{ bullet:{ code:"2022", indent:18 }, color: textColor||SLATE, breakLine:true, paraSpaceAfter: 10, fontSize:14.5, fontFace:BFONT } }));
  s.addText(items, { x, y, w, h: arr.length*0.55+0.2, isTextBox:true, margin:0, valign:"top", color: textColor||SLATE });
}

// attach notes
p.slides.forEach((sl,i)=>{ if(notes[i]) sl.addNotes(notes[i]); });

const out = process.argv[2] || "deck.pptx";
p.writeFile({ fileName: out }).then(f=>console.log("wrote", f));
