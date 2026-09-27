const pptxgen = require("pptxgenjs");
const p = new pptxgen();
p.layout = "LAYOUT_WIDE";
const W = 13.333;

// Technical palette (slate + indigo, dark code cards)
const INK = "111827", NAVY = "1F2937", SLATE = "374151", MUTE = "6B7280";
const LIGHT = "F5F7FB", CARD = "FFFFFF", LINE = "E4E8F0";
const ACC = "4F46E5", ACC2 = "0E9F9F", GOOD = "1E9E6A", WARN = "D97706", BAD = "DC2626", PURP = "7C3AED";
const CODEBG = "0F172A", CODEFG = "E2E8F0", CODEKEY = "7DD3FC", CODECMT = "64748B", CODESTR = "86EFAC";
const H = "微软雅黑", MONO = "Consolas";
const notes = [];

function bgLight(s){ s.background = { color: LIGHT }; }
function bgDark(s){ s.background = { color: INK }; }
function sh(){ return { type:"outer", color:"9AA6D6", opacity:0.28, blur:9, offset:3, angle:90 }; }
function kicker(s,t,c){ s.addText(t,{x:0.9,y:0.55,w:11,h:0.35,fontFace:H,fontSize:12.5,color:c||ACC,bold:true,charSpacing:2,isTextBox:true,margin:0}); }
function title(s,t){ s.addText(t,{x:0.9,y:0.9,w:11.5,h:0.85,fontFace:H,fontSize:30,bold:true,color:INK,isTextBox:true,margin:0}); }
function page(s,n){ s.addText(String(n).padStart(2,"0"),{x:12.35,y:6.98,w:0.7,h:0.3,align:"right",fontFace:MONO,fontSize:10,color:MUTE,isTextBox:true,margin:0}); }
function card(s,x,y,w,h,fill){ s.addShape(p.ShapeType.roundRect,{x,y,w,h,rectRadius:0.08,fill:{color:fill||CARD},line:{color:LINE,width:1},shadow:sh()}); }
function tag(s,x,y,t,c){ s.addShape(p.ShapeType.roundRect,{x,y,w:0.28+t.length*0.13,h:0.34,rectRadius:0.06,fill:{color:c}}); s.addText(t,{x,y,w:0.28+t.length*0.13,h:0.34,align:"center",valign:"middle",fontFace:H,fontSize:11,bold:true,color:"FFFFFF",isTextBox:true,margin:0}); }

// code card: array of {t, c?} lines; monospace on dark bg
function code(s,x,y,w,h,lines,titleTxt){
  s.addShape(p.ShapeType.roundRect,{x,y,w,h,rectRadius:0.06,fill:{color:CODEBG},shadow:sh()});
  if(titleTxt){ s.addText(titleTxt,{x:x+0.25,y:y+0.12,w:w-0.5,h:0.3,fontFace:MONO,fontSize:11,color:CODECMT,isTextBox:true,margin:0}); }
  const startY = y + (titleTxt?0.5:0.22);
  const rt = lines.map(l => ({ text: l.t, options:{ fontFace:MONO, fontSize: l.sz||12, color: l.c||CODEFG, breakLine:true, bold:!!l.b } }));
  s.addText(rt, { x:x+0.25, y:startY, w:w-0.5, h:h-(startY-y)-0.15, isTextBox:true, margin:0, valign:"top", lineSpacingMultiple:1.06 });
}
function bullets(s,x,y,w,arr,color){
  const items = arr.map(t=>({text:t,options:{bullet:{code:"2022",indent:16},color:color||SLATE,breakLine:true,paraSpaceAfter:7,fontSize:13.5,fontFace:H}}));
  s.addText(items,{x,y,w,h:arr.length*0.5+0.2,isTextBox:true,margin:0,valign:"top"});
}

// ---------- 1 title ----------
(()=>{ const s=p.addSlide(); bgDark(s);
  s.addShape(p.ShapeType.roundRect,{x:0.9,y:1.7,w:0.55,h:0.55,rectRadius:0.1,fill:{color:ACC}});
  s.addText("</>",{x:0.9,y:1.7,w:0.55,h:0.55,align:"center",valign:"middle",fontFace:MONO,fontSize:16,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
  s.addText("技术设计与实现",{x:1.65,y:1.72,w:8,h:0.5,fontFace:H,fontSize:16,color:"A5B4FC",bold:true,charSpacing:2,isTextBox:true,margin:0});
  s.addText("AI 订阅统一分发网关",{x:0.9,y:2.5,w:11.4,h:1.1,fontFace:H,fontSize:46,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
  s.addText("架构 · 数据面 · 日志存储 · 本次技术改动（可插拔 Blob 后端）",{x:0.9,y:3.7,w:11.4,h:0.6,fontFace:H,fontSize:20,color:"CBD5E1",isTextBox:true,margin:0});
  s.addText([
    {text:"Go 1.26",options:{fontFace:MONO,fontSize:13,color:"93C5FD",breakLine:false}},
    {text:"   ·   PostgreSQL 16   ·   CLIProxyAPI   ·   React   ·   Docker",options:{fontFace:MONO,fontSize:13,color:"64748B",breakLine:true}},
  ],{x:0.9,y:4.6,w:11,h:0.4,isTextBox:true,margin:0});
  s.addText("云涌世纪 · 工程团队 · 2026-09",{x:0.9,y:6.7,w:8,h:0.35,fontFace:H,fontSize:12,color:"64748B",isTextBox:true,margin:0});
  notes.push("技术版汇报。今天讲三块：整体架构、日志存储机制、以及本次要做的具体技术改动——把对话原文的 blob 存储做成可插拔后端（Postgres 或 S3/R2）。");
})();

// ---------- 2 architecture ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"系统架构"); title(s,"两层：自研网关 + 原版 CPA");
  const y=2.5,bh=1.4,bw=2.45,gap=0.5,x0=0.95;
  const boxes=[["Client","Codex / Cursor\nchat & responses","8A93C0"],["Gateway (Go)","鉴权·并发·指纹\n日志·API·SPA",ACC],["CLIProxyAPI","OAuth 刷新\n上游协议",ACC2],["OpenAI","每账号固定\n出口代理",NAVY]];
  boxes.forEach((b,i)=>{const x=x0+i*(bw+gap);
    s.addShape(p.ShapeType.roundRect,{x,y,w:bw,h:bh,rectRadius:0.08,fill:{color:b[2]},shadow:sh()});
    s.addText(b[0],{x,y:y+0.2,w:bw,h:0.4,align:"center",fontFace:H,fontSize:15,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
    s.addText(b[1],{x,y:y+0.66,w:bw,h:0.65,align:"center",fontFace:H,fontSize:11,color:"EEF2FF",isTextBox:true,margin:0,valign:"top"});
    if(i<3) s.addShape(p.ShapeType.rightArrow,{x:x+bw+0.06,y:y+bh/2-0.13,w:gap-0.12,h:0.26,fill:{color:MUTE}});
  });
  // module map
  card(s,0.95,4.35,11.45,2.05);
  s.addText("网关内部模块（internal/*）",{x:1.2,y:4.5,w:8,h:0.35,fontFace:H,fontSize:14,bold:true,color:INK,isTextBox:true,margin:0});
  const mods=["proxy 数据面/并发/指纹","capture 解析/去重/脱敏","recorder 异步落库","store 仓储(PG)","cpa CPA管理API","health 健康/额度","feishu 登录/同步","web 后台/RBAC","report 日报","jobs 定时","blobstore S3/R2","secure 加密/Key"];
  const cols=4,cw=2.68,cx=1.2,cy=4.95,chh=0.42;
  mods.forEach((m,i)=>{const c=i%cols,r=Math.floor(i/cols);const x=cx+c*cw,yy=cy+r*chh;
    s.addText([{text:"▹ ",options:{color:ACC,fontFace:H,fontSize:11.5,breakLine:false}},{text:m,options:{color:SLATE,fontFace:H,fontSize:11.5,breakLine:true}}],{x,y:yy,w:cw-0.1,h:chh,isTextBox:true,margin:0,valign:"middle"});
  });
  page(s,2);
  notes.push("网关是我们写的、承载全部公司逻辑；CPA 是成熟开源项目保持原版跟随上游。网关内部按职责分成十来个 internal 包，每个包单一职责、可独立测试。");
})();

// ---------- 3 data plane lifecycle ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"数据面"); title(s,"/v1 请求生命周期");
  const steps=[
    ["1 鉴权","Bearer sk-emp → 哈希查 Key → 员工/账号/告知/限额校验"],
    ["2 识别","按 UA/Originator 分 codex / other，记 observed_clients"],
    ["3 并发闸","每账号 FIFO 信号量，容量 4，满则排队(≤60s)"],
    ["4 指纹","codex 透传真实头 / other 套设备档案 + 会话ID"],
    ["5 改写转发","model → acct-<id>/<model>，POST 到 CPA(内部Key)"],
    ["6 流式回传","SSE 逐块 flush 给客户端，同时 tee 进解析器"],
    ["7 记日志","解析用量/模型/TTFT → 异步 Recorder.Submit"],
    ["8 反应","上游429→限额 / 401→重登 / 模型不符→降智信号"],
  ];
  const cw=5.62,chh=0.92,gx=0.95,sx=0.5,gy=2.35,sy=0.2;
  steps.forEach((st,i)=>{const c=i%2,r=Math.floor(i/2);const x=gx+c*(cw+sx),y=gy+r*(chh+sy);
    card(s,x,y,cw,chh);
    s.addShape(p.ShapeType.roundRect,{x:x+0.2,y:y+0.24,w:0.16,h:chh-0.48,rectRadius:0.06,fill:{color:ACC}});
    s.addText(st[0],{x:x+0.45,y:y+0.12,w:1.75,h:chh-0.24,fontFace:H,fontSize:15,bold:true,color:INK,isTextBox:true,margin:0,valign:"middle"});
    s.addText(st[1],{x:x+2.15,y:y+0.12,w:cw-2.35,h:chh-0.24,fontFace:H,fontSize:11.5,color:SLATE,isTextBox:true,margin:0,valign:"middle"});
  });
  page(s,3);
  notes.push("一次请求经过八步。名额在流结束才释放；客户端断开会取消上游请求、释放名额，不空占。");
})();

// ---------- 4 one-per-account ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"隔离机制"); title(s,"一人一号：前缀路由做真隔离");
  bullets(s,0.95,2.35,5.6,[
    "每账号唯一前缀 acct-<id>，写入 CPA 账号文件",
    "网关把 model 改写成 acct-<id>/<model> 再转发",
    "CPA 开 force-model-prefix：该前缀只命中这一个账号",
    "员工拿不到 CPA、碰不到前缀 → 前缀即隔离边界",
    "账号失效则请求失败，绝不回落到他人账号",
  ]);
  code(s,6.8,2.35,5.55,3.6,[
    {t:"// 网关侧：改写模型名",c:CODECMT},
    {t:"model = \"acct-\"+acc.ID+\"/\"+model",c:CODEFG},
    {t:"POST {CPA}/v1/responses",c:CODEFG},
    {t:"  Authorization: Bearer <内部Key>",c:CODESTR},
    {t:"",},
    {t:"# CPA config.yaml",c:CODECMT},
    {t:"force-model-prefix: true",c:CODEKEY},
    {t:"# auth 文件: prefix = acct-42",c:CODECMT},
    {t:"→ acct-42/gpt-5.1 只命中账号 42",c:CODESTR},
  ],"model prefix routing");
  page(s,4);
  notes.push("隔离不是靠信任，是靠 CPA 的强制前缀路由 + 员工够不到 CPA。一个 Key 只可能打到它绑定的那个账号。");
})();

// ---------- 5 limiter ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"并发控制"); title(s,"每账号 FIFO 信号量（容量 4）");
  bullets(s,0.95,2.35,5.6,[
    "每账号一个内存信号量，默认容量 4，可单独覆盖",
    "满员时进 FIFO 队列，最长等 60 秒（可配）",
    "release 时把名额直接交给队首（hand-off），杜绝抢占",
    "ctx 取消/超时会从队列摘除并归还名额，零泄漏",
    "已用 -race 压测：100 并发下峰值恒 ≤ 4",
  ]);
  code(s,6.8,2.35,5.55,3.6,[
    {t:"func (l *Limiter) Acquire(",c:CODEKEY},
    {t:"  ctx, acct, capacity, maxWait,",c:CODEFG},
    {t:") (release func(), waited, err)",c:CODEKEY},
    {t:"",},
    {t:"// 释放：优先交给队首等待者",c:CODECMT},
    {t:"if w := q.front(); w != nil {",c:CODEFG},
    {t:"    w.ready <- struct{}{}   // 名额直接转交",c:CODESTR},
    {t:"} else { inflight-- }",c:CODEFG},
  ],"internal/proxy/limiter.go");
  page(s,5);
  notes.push("这是防降智的核心。hand-off 语义保证公平且无抢占；取消安全归还名额，压测下并发恒不超 4。");
})();

// ---------- 6 log storage 3-tier ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"日志存储"); title(s,"三层：元数据 / 原文块 / 检索文本");
  const rows=[
    ["元数据","requests 表","人·账号·模型·token·耗时·状态 + 片段哈希列表","Postgres · 永久", ACC],
    ["原文块","blobs","去重 + zstd 压缩的对话内容（大头）","PG 或 S3/R2 · 180 天", PURP],
    ["检索文本","request_texts","抽取的用户输入/回复，pg_trgm 中文检索","Postgres · 180 天", ACC2],
  ];
  let y=2.4;const x=0.95,w=11.45,hh=1.15;
  rows.forEach(r=>{card(s,x,y,w,hh);
    s.addShape(p.ShapeType.roundRect,{x:x+0.25,y:y+0.24,w:0.16,h:hh-0.48,rectRadius:0.06,fill:{color:r[4]}});
    s.addText(r[0],{x:x+0.55,y:y+0.14,w:2.0,h:hh-0.28,fontFace:H,fontSize:16,bold:true,color:INK,isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[1],{x:x+2.6,y:y+0.14,w:2.2,h:hh-0.28,fontFace:MONO,fontSize:12.5,color:r[4],isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[2],{x:x+4.9,y:y+0.14,w:4.6,h:hh-0.28,fontFace:H,fontSize:12,color:SLATE,isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[3],{x:x+9.5,y:y+0.14,w:1.8,h:hh-0.28,fontFace:H,fontSize:11,color:MUTE,isTextBox:true,margin:0,valign:"middle"});
    y+=hh+0.2;
  });
  s.addText("关键：只有“原文块（blobs）”是可增长的大头，也只有它能挪到对象存储；元数据与检索文本必须留在 Postgres。",
    {x:0.95,y:6.2,w:11.45,h:0.5,fontFace:H,fontSize:12.5,italic:true,color:MUTE,isTextBox:true,margin:0});
  page(s,6);
  notes.push("这页是理解本次改动的前提：三类数据里只有 blobs 是大头且冷读，正好适合对象存储；另外两类要关系查询，留在 PG。");
})();

// ---------- 7 dedup + prefix chain ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"存储优化"); title(s,"片段去重 + 会话前缀链编码");
  bullets(s,0.95,2.35,5.6,[
    "Codex 每轮重发完整历史 → 直接存会到 TB 级",
    "拆成片段(instructions/tools/input/output)，按 sha256 去重",
    "同一段历史发 100 次，blobs 只存一份",
    "requests 只存有序哈希列表；本轮以上一轮为前缀时只存增量",
    "还原时沿 req_prefix_of 链回溯拼接",
  ]);
  code(s,6.8,2.35,5.55,3.6,[
    {t:"// 会话内前缀压缩",c:CODECMT},
    {t:"prev = [h_sys, h_a, h_b]",c:CODEFG},
    {t:"curr = [h_sys, h_a, h_b, h_c]",c:CODEFG},
    {t:"→ store: prefix_of=prevID,",c:CODESTR},
    {t:"         prefix_len=3,",c:CODESTR},
    {t:"         hashes=[h_c]   // 仅增量",c:CODESTR},
    {t:"",},
    {t:"实测：落库体量 ≈ 原始的几个百分点",c:CODEKEY},
  ],"internal/capture/encode.go");
  page(s,7);
  notes.push("两层压缩：内容片段去重 + 会话前缀链只存增量。这已经把 90%+ 的体量砍掉，对象存储是在此之上再省。");
})();

// ---------- 8 THE CHANGE: pluggable blob backend ----------
(()=>{ const s=p.addSlide(); bgLight(s);
  s.addShape(p.ShapeType.roundRect,{x:0.9,y:0.5,w:1.5,h:0.4,rectRadius:0.08,fill:{color:WARN}});
  s.addText("本次技术改动",{x:0.9,y:0.5,w:1.5,h:0.4,align:"center",valign:"middle",fontFace:H,fontSize:12,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
  title(s,"可插拔 Blob 后端：Postgres | S3/R2");
  // before / after
  card(s,0.95,2.3,5.6,3.9); s.addText("改动前",{x:1.2,y:2.45,w:5,h:0.4,fontFace:H,fontSize:16,bold:true,color:BAD,isTextBox:true,margin:0});
  bullets(s,1.2,2.95,5.1,[
    "blobs.data_zstd 直接存在 Postgres",
    "原文随用量增长，占系统盘",
    "小硬盘(40–80G)几个月就撑爆",
    "撑爆 → Postgres 宕，牵连整个系统",
    "SSD 约 $0.10/GB/月",
  ]);
  card(s,6.75,2.3,5.6,3.9); s.addText("改动后",{x:7.0,y:2.45,w:5,h:0.4,fontFace:H,fontSize:16,bold:true,color:GOOD,isTextBox:true,margin:0});
  bullets(s,7.0,2.95,5.1,[
    "字节存对象存储(S3/R2/OSS)，元数据仍在 PG",
    "去重/GC/检索逻辑完全不变",
    "生命周期规则自动 180 天过期",
    "系统盘只留元数据，机器可长期小配置运行",
    "R2 约 $0.015/GB/月 + 出流量免费",
  ], GOOD);
  page(s,8);
  notes.push("这就是本次要做的具体改动。核心价值不只是每 GB 便宜，而是把不断增长的原文从昂贵的小系统盘挪走，让小机器长期扛得住。");
})();

// ---------- 9 blob backend: interface & flow ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"本次技术改动 · 实现"); title(s,"一个接口，两种后端");
  code(s,0.95,2.3,5.7,4.0,[
    {t:"// internal/store —— 抽象字节存储",c:CODECMT},
    {t:"type BlobBackend interface {",c:CODEKEY},
    {t:"  Put(ctx, key string, data []byte) error",c:CODEFG},
    {t:"  Get(ctx, key string) ([]byte, error)",c:CODEFG},
    {t:"  Delete(ctx, keys []string) error",c:CODEFG},
    {t:"}",c:CODEKEY},
    {t:"// nil = 存 Postgres(默认)",c:CODECMT},
    {t:"// 设置 = 存 S3/R2/OSS",c:CODECMT},
    {t:"st.SetBlobBackend(blobstore.NewS3(cfg))",c:CODESTR},
  ],"BlobBackend");
  card(s,6.85,2.3,5.5,4.0);
  s.addText("PutBlobs 的分支（去重不变）",{x:7.1,y:2.45,w:5,h:0.35,fontFace:H,fontSize:14,bold:true,color:INK,isTextBox:true,margin:0});
  bullets(s,7.1,2.95,5.0,[
    "元数据 upsert 返回是否“新插入”(xmax=0)",
    "仅新片段才上传字节 → 保住去重的写节省",
    "外部后端行 data_zstd=NULL 作标记",
    "GetBlob：行内有字节走 PG，否则走后端",
    "Purge：先删桶对象，再删 PG 行",
    "新旧行可共存 → 平滑迁移，无需回填",
  ]);
  page(s,9);
  notes.push("只加了一个三方法接口 + 一个 SetBlobBackend。去重靠 upsert 的 xmax=0 判断只对新片段上传，写节省不丢。新旧行共存，迁移零风险。");
})();

// ---------- 10 config & migration ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"本次技术改动 · 配置与迁移"); title(s,"一个开关即可切换");
  code(s,0.95,2.3,6.4,3.15,[
    {t:"# .env —— 默认 postgres，无需任何改动",c:CODECMT},
    {t:"BLOB_BACKEND=postgres",c:CODEKEY},
    {t:"",},
    {t:"# 切到 Cloudflare R2（示例）",c:CODECMT},
    {t:"BLOB_BACKEND=s3",c:CODEKEY},
    {t:"S3_ENDPOINT=<acct>.r2.cloudflarestorage.com",c:CODEFG},
    {t:"S3_REGION=auto   S3_BUCKET=ai-seat-logs",c:CODEFG},
    {t:"S3_ACCESS_KEY=…  S3_SECRET_KEY=…",c:CODEFG},
  ],".env");
  code(s,0.95,5.6,6.4,0.95,[
    {t:"-- migration 00002",c:CODECMT},
    {t:"ALTER TABLE blobs ALTER COLUMN data_zstd DROP NOT NULL;",c:CODESTR},
  ]);
  card(s,7.55,2.3,4.8,4.25);
  s.addText("要点",{x:7.8,y:2.45,w:4,h:0.35,fontFace:H,fontSize:14,bold:true,color:INK,isTextBox:true,margin:0});
  bullets(s,7.8,2.95,4.3,[
    "S3 客户端用 minio-go，兼容 R2/S3/OSS/MinIO",
    "启动即校验桶可达（凭证错则快速失败）",
    "验证阶段用 postgres，上生产切 s3",
    "推荐 R2：你已用 Cloudflare，出流量免费",
    "in-memory 假后端单测覆盖：外部路径/去重/清理",
    "全套 go test -race 通过（15/15 包）",
  ]);
  page(s,10);
  notes.push("对使用者就是一个环境变量。迁移只把 data_zstd 放开为可空。切 R2 因为你们已经在用 Cloudflare、出流量免费。改动有单测覆盖。");
})();

// ---------- 11 fingerprint ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"账号安全"); title(s,"指纹一致 + 固定出口");
  const rows=[
    ["官方 Codex","透传真实请求头，只剥离 X-Forwarded/Via 等暴露代理的字段",GOOD],
    ["其他客户端","套账号固定设备档案：UA/Version/安装ID + 稳定会话ID",ACC],
    ["出口 IP","每账号 proxy_url 固定，分组 5–10 号一 IP，30 分钟自检",PURP],
    ["降智监测","响应模型≠请求模型 / 推理量被封顶 → 标红告警",WARN],
    ["不做 TLS 伪装","保持一致而非伪装官方二进制（立场 + 维护成本考量）",MUTE],
  ];
  let y=2.35;const x=0.95,w=11.45,hh=0.82;
  rows.forEach(r=>{card(s,x,y,w,hh);
    s.addShape(p.ShapeType.roundRect,{x:x+0.25,y:y+0.2,w:0.14,h:hh-0.4,rectRadius:0.05,fill:{color:r[2]}});
    s.addText(r[0],{x:x+0.55,y:y+0.1,w:3.0,h:hh-0.2,fontFace:H,fontSize:14.5,bold:true,color:INK,isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[1],{x:x+3.7,y:y+0.1,w:w-4.0,h:hh-0.2,fontFace:H,fontSize:12,color:SLATE,isTextBox:true,margin:0,valign:"middle"});
    y+=hh+0.16;
  });
  page(s,11);
  notes.push("指纹策略：官方客户端透传、其他套档案、出口 IP 固定、降智监测。刻意不伪造 TLS——保持一致、不主动伪装。");
})();

// ---------- 12 data model ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"数据模型"); title(s,"核心表（PostgreSQL 16）");
  const t=[
    ["employees / departments","飞书同步，open_id 认人，部门树支撑 RBAC"],
    ["upstream_accounts","账号↔员工唯一绑定，device_profile，ip_group"],
    ["api_keys","sk-emp 哈希存储，一员工一活跃 Key（部分唯一索引）"],
    ["requests (按月分区就绪)","每请求元数据 + 前缀链编码字段"],
    ["blobs","hash 主键，data_zstd 可空（NULL=外部后端）"],
    ["request_texts","pg_trgm gin 索引，中文子串检索"],
    ["account_signals / quota_snapshots","健康信号 / 额度快照"],
    ["audit_logs","只追加，触发器禁止 UPDATE/DELETE"],
  ];
  const cw=5.62,chh=0.86,gx=0.95,sx=0.5,gy=2.35,sy=0.16;
  t.forEach((r,i)=>{const c=i%2,rr=Math.floor(i/2);const x=gx+c*(cw+sx),y=gy+rr*(chh+sy);
    card(s,x,y,cw,chh);
    s.addText(r[0],{x:x+0.3,y:y+0.1,w:cw-0.6,h:0.4,fontFace:MONO,fontSize:12.5,bold:true,color:ACC,isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[1],{x:x+0.3,y:y+0.44,w:cw-0.6,h:0.36,fontFace:H,fontSize:11,color:SLATE,isTextBox:true,margin:0,valign:"middle"});
  });
  page(s,12);
  notes.push("表结构要点：一员工一活跃 Key 用部分唯一索引保证；audit_logs 用触发器强制只追加；blobs.data_zstd 可空区分内外部存储。");
})();

// ---------- 13 deploy ----------
(()=>{ const s=p.addSlide(); bgLight(s); kicker(s,"部署"); title(s,"拓扑与选型");
  code(s,0.95,2.35,6.5,3.0,[
    {t:"caddy      :443  自动 TLS，反代 gateway",c:CODEFG},
    {t:"gateway    :8080 go:embed 前端，单二进制",c:CODEFG},
    {t:"cpa        内网  不发布端口，账号走各自代理",c:CODEFG},
    {t:"postgres   内网  数据卷 + 每日备份",c:CODEFG},
    {t:"",},
    {t:"网络：edge / backend(有出口) / data(内网隔离)",c:CODECMT},
    {t:"blobs → S3/R2（生产）",c:CODESTR},
  ],"docker-compose");
  card(s,7.65,2.35,4.7,3.0);
  s.addText("规格建议",{x:7.9,y:2.5,w:4,h:0.35,fontFace:H,fontSize:14,bold:true,color:INK,isTextBox:true,margin:0});
  bullets(s,7.9,3.0,4.2,[
    "试点 ≤10 人：2C4G / 60–80G",
    "生产 20–50 人：4C8G / 120–200G",
    "生产 50–100 人：4C16G + blobs 走 R2",
    "服务器美国节点，OpenAI 友好",
    "出口用干净静态住宅 IP（账号安全核心）",
  ]);
  s.addText("升级 CPA：改镜像 tag 重启即可；网关升级换二进制/镜像。",
    {x:0.95,y:5.55,w:11.4,h:0.4,fontFace:H,fontSize:12,italic:true,color:MUTE,isTextBox:true,margin:0});
  page(s,13);
  notes.push("三网络隔离：只有 caddy 对外，cpa/postgres 不发布端口，postgres 无外网出口。blobs 上生产走 R2。出口 IP 才是账号安全核心。");
})();

// ---------- 14 testing + close ----------
(()=>{ const s=p.addSlide(); bgDark(s);
  s.addText("质量与后续",{x:0.9,y:0.7,w:8,h:0.5,fontFace:H,fontSize:15,color:"A5B4FC",bold:true,charSpacing:2,isTextBox:true,margin:0});
  s.addText("测试策略 & 上线前验证",{x:0.9,y:1.25,w:11,h:0.8,fontFace:H,fontSize:32,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
  const L=[
    ["单元/竞态","全部包 go test -race 通过；限流器/编码/脱敏/解析表驱动覆盖"],
    ["集成","假 CPA 上游断言：前缀路由、峰值并发=4、错误映射、流式透传"],
    ["Blob 后端","in-memory 假后端验证外部路径、去重、清理"],
    ["端到端","serve 二进制实测：登录→发Key→限并发→日志→看板"],
    ["上线前(预发)","真实账号清单：OAuth 登录、真实额度头、token 刷新走代理、降智观察"],
  ];
  let y=2.35;
  L.forEach((r,i)=>{
    s.addShape(p.ShapeType.ellipse,{x:0.95,y:y+0.03,w:0.34,h:0.34,fill:{color:ACC}});
    s.addText(String(i+1),{x:0.95,y:y+0.03,w:0.34,h:0.34,align:"center",valign:"middle",fontFace:H,fontSize:13,bold:true,color:"FFFFFF",isTextBox:true,margin:0});
    s.addText(r[0],{x:1.45,y:y-0.02,w:2.5,h:0.45,fontFace:H,fontSize:15,bold:true,color:"FFFFFF",isTextBox:true,margin:0,valign:"middle"});
    s.addText(r[1],{x:4.0,y:y-0.02,w:8.4,h:0.45,fontFace:H,fontSize:12.5,color:"CBD5E1",isTextBox:true,margin:0,valign:"middle"});
    y+=0.62;
  });
  s.addText("下一步：验证环境接飞书登录 + 假上游跑通 → 一个真号 + 干净美国 IP 验证 Codex → 生产 blobs 切 R2",
    {x:0.95,y:5.75,w:11.4,h:0.7,fontFace:H,fontSize:13,color:"93C5FD",isTextBox:true,margin:0,valign:"top"});
  page(s,14);
  notes.push("质量：竞态测试 + 假CPA集成 + blob后端单测 + serve端到端。上线前还有真号验证清单。下一步就是验证环境跑通、真号验证、生产切 R2。");
})();

p.slides.forEach((sl,i)=>{ if(notes[i]) sl.addNotes(notes[i]); });
const out = process.argv[2] || "tech.pptx";
p.writeFile({ fileName: out }).then(f=>console.log("wrote", f));
