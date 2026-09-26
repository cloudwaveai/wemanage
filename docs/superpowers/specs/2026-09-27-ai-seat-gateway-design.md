# AI 订阅网关（ai-seat-gateway）设计文档

- 日期：2026-09-27
- 状态：待评审
- 方案：自研网关在前 + 原版 CLIProxyAPI（CPA）在后

## 1. 背景与目标

公司为每位员工购买一个 ChatGPT Pro 订阅，但不直接把账号或报销交给员工，而是通过自建网关统一分发：

1. **一人一号**：员工拿到的是公司签发的 API Key，每个 Key 固定路由到该员工专属的上游账号，不做号池轮询。
2. **全量日志**：请求与响应原文、token 用量、耗时全部留存在公司，用于用量管理与工作情况分析。
3. **并发上限**：每个上游账号同时在途请求默认不超过 4 个，降低风控和"降智"风险。
4. **管理员管控**：账号只能由管理员添加和绑定，员工无法修改。
5. **指纹一致**：发往 OpenAI 的请求在请求头、会话标识、出口 IP 上稳定、自洽。

### 非目标（第一期不做）

- 员工自行添加额外订阅（数据表预留 `owner_type` 字段）。
- Claude Pro/Max 订阅接入。Anthropic 条款明确禁止第三方中转订阅凭证，并自 2026-01 起做服务端封堵。
- Responses API 的 WebSocket 通道。第一期只支持 HTTP + SSE。
- TLS 指纹伪装成官方 codex-rs。原因见 §5.4。
- 多实例高可用、计费结算。

### 规模与部署约束

- 20–100 名员工，一人一个 Pro 账号。
- 一台新开的海外服务器，Docker Compose 部署。
- 出口 IP 按组固定：每个静态 IP 带 5–10 个账号。

## 2. 调研结论

对照 CPA v7.3.18（2026-09-26，`ed980be`）源码核对。

| 能力 | 原版 CPA | 本设计的做法 |
|---|---|---|
| Key → 单账号绑定 | 无。`api-keys` 为扁平列表，所有 Key 共享号池 | 网关负责绑定；用账号前缀 `prefix` + `force-model-prefix` 让 CPA 只选中一个账号 |
| 每账号并发上限 | 无。issue #4031 维护者拒绝内置 | 网关内存信号量 |
| 全量日志 | `request-log` 按请求写文本文件，有总量上限；v6.10 起移除用量统计 | 网关自行落库（去重存储） |
| 多用户、权限 | 无，只有一个管理密钥 | 网关 + 飞书 |
| 每账号出口 IP | 支持，账号文件字段 `proxy_url` | 网关写入 IP 组代理地址 |
| UA / Originator | 默认覆写为常量 `codex-tui/0.154.0 (Mac OS …) iTerm…`，与透传的 `Version` 等请求头矛盾 | `codex.disable-codex-cloaking: true`，由网关决定请求头 |
| TLS 指纹 | 对 chatgpt.com 使用 uTLS `HelloChrome_Auto`，不可配置 | 保持不动 |

### OpenAI 公开表态（Thibault Sottiaux 推文原文）

- 2026-05-23：可以在其他工具里使用 ChatGPT 账号登录。
- 2026-08-21：把订阅变成 API 流量再转供或多人共享不受支持，会被反欺诈系统标记；用官方或开源客户端登录没问题。

**结论**：一人一号不属于"多人共享"，但"订阅经网关变为 API"仍是灰色地带。第三方客户端走 chat completions 风险最高，Codex 官方客户端风险最低。因此员工自助页默认推荐 Codex 配置，第三方客户端在看板中单独标记。

## 3. 架构

```
员工客户端（Codex CLI/IDE、Cursor、Cherry Studio…）
      │ HTTPS  Authorization: Bearer sk-emp-xxx
      ▼
 Caddy（TLS）
      ▼
 gateway（Go 单进程）
   ├─ /v1/*   数据面：鉴权 → 绑定 → 并发闸 → 指纹处理 → 转发 → 记日志
   ├─ /api/*  管理面：管理后台 + 员工自助页（飞书登录）
   ├─ /       React SPA（go:embed）
   └─ 定时任务：CPA 同步、账号健康、出口 IP 检查、AI 日报、原文清理
      │ 内网 HTTP，model = acct-<账号ID>/<模型>
      ▼
 CPA（原版镜像，仅内网）
      │ 账号文件 proxy_url = 所属 IP 组代理
      ▼
 chatgpt.com/backend-api/codex
```

容器共 4 个：`caddy`、`gateway`、`cpa`、`postgres`。

### 边界原则

1. 员工无法访问 CPA。CPA 端口不对外暴露，管理密钥和凭证目录只有网关持有。
2. 网关数据库是唯一的数据来源。账号文件上的 `prefix` 和 `proxy_url` 由网关写入；同步任务发现两边不一致时告警，不静默修正。
3. OAuth token 只存在 CPA 的凭证目录，由 CPA 负责刷新；网关只保存 `cpa_auth_id`。
4. 需要在飞书开放平台建一个企业自建应用，用于网页登录、读取通讯录（部门和负责人）、机器人推送。

### CPA 关键配置

```yaml
api-keys: ["<仅网关持有的内部 key>"]
force-model-prefix: true      # 不带前缀的请求只能用不带前缀的账号；我们的账号全部带前缀，所以不带前缀的请求会直接失败
routing:
  strategy: fill-first        # 每个前缀只对应一个账号，策略不影响结果
codex:
  disable-codex-cloaking: true
request-log: false            # 日志由网关负责；排障时可临时打开
remote-management:
  secret-key: "<仅网关持有>"
passthrough-headers: true     # 把经过 CPA 过滤后的上游响应头回传给网关，用于读取额度；额度头能否通过过滤需实测，见 §11
```

## 4. 数据面：请求处理流程

### 4.1 鉴权

- Key 格式为 `sk-emp-` 加 32 字节随机串（base62）。数据库只存 SHA-256 和前 8 位（用于展示）。
- 以下任一情况都直接返回 OpenAI 格式的错误，不转发：
  - Key 无效或已撤销：401；
  - 员工已停用或未确认告知：403；
  - 没有绑定账号，或账号状态不可用：403 或 503，错误信息里写明原因。

### 4.2 客户端识别

- 根据 `User-Agent` 和 `Originator` 把请求归为 `codex`（CLI、TUI、IDE、exec）或 `other`。
- 识别规则做成可配置的正则表，结果写入请求记录的 `client_kind` 字段。

### 4.3 并发闸

- 每个账号一个 FIFO 信号量，默认容量 4，可按账号单独覆盖。
- 请求排队最长等待默认 60 秒，可配置；超时返回 429，并带 `Retry-After`。
- **名额在上游响应流结束时才释放**。客户端中途断开时，要取消发往上游的请求（context 取消），然后释放名额。
- 排队时长写入 `queued_ms`，看板里据此判断并发上限是否过紧。
- 只有单进程，信号量放在内存里即可。

### 4.4 指纹处理

| 客户端类型 | 处理方式 |
|---|---|
| `codex` | 透传全部请求头：UA、Originator、Version、Session-Id、Thread-Id、X-Codex-*、请求体里的 `client_metadata`。只删除 `X-Forwarded-*`、`X-Real-IP`、`Forwarded`、`Via` |
| `other` | 套用该账号的**设备档案**，内容见下 |

设备档案在账号创建时生成，之后固定不变，包括：
- OS 描述，添加账号时由管理员选择；
- `installation_id`（UUID）；
- UA、Originator、Version 的模板，由全局 Codex 版本号渲染。

会话 ID 按对话生成：取"员工 ID + 对话首条用户消息"的哈希。这样同一段对话始终是同一个会话 ID，不同对话的会话 ID 不同。

**全局 Codex 版本号**
- 设置页展示最近 7 天员工真实 Codex 流量里出现最多的 UA 和 Version，管理员可一键采用。模板因此始终和真实客户端的格式一致。
- 另外每天检查一次 GitHub 上 openai/codex 的最新正式版；发现当前配置落后时，在后台提醒管理员。

### 4.5 转发

- 把 `model` 改写为 `acct-<id>/<model>`，转发到 `http://cpa:8317`，使用内部 key。
- 响应按流式逐块 flush 回员工，同时 tee 一份给日志模块。
- `/v1/models`：向 CPA 查询该前缀下可用的模型，去掉前缀后返回。
- 支持的端点：`/v1/responses`、`/v1/responses/compact`、`/v1/chat/completions`、`/v1/models`。
- WebSocket 升级请求返回 426。自助页生成的 Codex 配置会关闭 WebSocket。

### 4.6 日志采集

- 流结束后，解析 Responses 的 `response.completed` 事件，或 chat completions 的最后一个 chunk，得到：
  - token 用量：输入、缓存命中、输出、推理；
  - 实际响应的模型；
  - 首字耗时（TTFT）、总耗时、状态码、错误内容。
- 日志先写入内存缓冲队列，由独立的 goroutine 批量写库，不阻塞响应。
- 数据库写失败时，日志追加写到本地 spool 文件（按行存 JSON），数据库恢复后回放，保证不丢。

### 4.7 上游错误处理

| 情况 | 返回给员工 | 系统动作 |
|---|---|---|
| 上游 429（额度用完） | 原样透传 | 账号状态标为"限额中"，记录恢复时间 |
| 上游 401 / 403（token 失效） | 503，提示"账号需重新登录，已通知管理员" | 账号状态标为"需重新登录"，飞书通知管理员 |
| CPA 不可达 | 502 | 告警 |
| 排队超时 | 429 + `Retry-After` | 计数，供看板展示 |
| 请求模型 ≠ 响应模型 | 正常返回 | 记一条 `model_mismatch` 信号 |

## 5. 账号管理

### 5.1 数据对象

- **员工**：从飞书同步。飞书停用的员工，其 Key 自动失效。
- **上游账号**：字段包括 `owner_type`（company / employee，第一期只有 company）和 `employee_id`（唯一，保证一对一绑定）。
- **IP 组**：代理地址（加密存储）和容量上限（默认 10）。

### 5.2 添加账号流程

1. 管理员选择 IP 组，容量已满的组不可选。
2. 网关调用 CPA 的 `GET /v0/management/codex-auth-url`，把登录链接展示给管理员。
3. 管理员在浏览器里完成 ChatGPT 登录。回调会跳到 `localhost:1455/...`，页面打不开属正常。管理员把完整的回调 URL 粘贴回后台，网关把它转交给 CPA，完成 token 交换。
4. 网关轮询 `GET /auth-files`，拿到新增的账号文件，然后：
   - 调 `PATCH /auth-files/fields` 写入 `prefix=acct-<id>` 和 `proxy_url`；
   - 记录邮箱和套餐类型；
   - 生成设备档案。
5. 管理员选择要绑定的员工。
6. 另一条路径：直接导入已有的凭证 JSON（比如 `~/.codex/auth.json`），网关通过 `POST /auth-files` 写入 CPA。

### 5.3 IP 组

- 账号可以换组，但需要二次确认（提示"更换出口 IP 本身是风险信号"），并记入审计。
- 每 30 分钟经各组代理访问一次 IP 回显服务。实际出口 IP 与记录值不一致时，记一条 `egress_changed` 信号并发飞书告警。

### 5.4 为什么不伪装 TLS 指纹

1. 需要跟着 codex-rs 的每个版本重新抓包，维护成本高。
2. TLS 只是众多信号之一。一人一号、固定 IP、用量正常这些账号层面的表现更关键。
3. 从"保持一致"变成"主动伪装"之后，一旦被 OpenAI 质询，公司"一人一个付费账号"的立场会变弱。

如有必要可以在后续阶段重新评估。

### 5.5 账号健康状态

| 状态 | 判定依据 |
|---|---|
| 正常 | 没有下面任何一种情况 |
| 限额中 | 上游 429，带恢复时间 |
| 需重新登录 | 上游 401 / 403，或 CPA 报告刷新失败 |
| 疑似降智 | 24 小时内 `model_mismatch` ≥ 3 次；或最近 20 次 `reasoning_effort` ≥ medium 的请求中，≥ 80% 的推理 token 数是同一个值（社区报告过被封顶在 516 之类的固定值） |
| 已停用 | 管理员手动停用 |

**额度百分比**：如果 CPA 透传回上游的 Codex 额度响应头（5 小时和每周的使用比例及重置时间），网关就记录快照，在员工页和后台展示。这一点需要实测，见 §11。

## 6. 日志存储

### 6.1 按片段去重

Codex 每一轮都会重发完整的历史。直接存原文，50 人 180 天会到 TB 级，所以按片段去重存储：

1. **拆分请求**：`instructions`、`tools` 定义、`input[]` / `messages[]` 里的每个元素，各自作为一个**片段**。
2. **响应**：只存最终组装好的 `output[]`，每个元素是一个片段。原始 SSE 流只在出错时保留。
3. **存储**：片段先做脱敏（§6.3），再对规范化后的 JSON 算 SHA-256，用 zstd 压缩后写入 `blobs`。内容相同的片段只存一次。
4. **请求记录**：只保存有序的片段哈希列表。查看时再拼回完整的请求和响应。
5. **标记新增片段**：同一会话里，上一次请求没出现过的片段标为 `is_new`。检索和日报都只看这部分。

预计存储量只有原始体积的百分之几。上线第一周用真实数据核对。

### 6.2 保留与清理

- 每个片段记录 `last_referenced_at`。每天凌晨删除 180 天内没被引用过的片段，以及 180 天前的片段引用行。
- `requests` 元数据、用量统计、日报、审计记录永久保留。
- `requests` 按月分区。

### 6.3 脱敏

- 只作用于落库前的日志副本；发往 OpenAI 的请求**不做任何修改**。
- 规则是可配置的正则表，内置以下规则：
  - `sk-` 开头的各类 key、`AKIA` 开头的 AWS key、`ghp_` / `github_pat_`、`xox[abpr]-`；
  - `-----BEGIN … PRIVATE KEY-----` 块、JWT（三段 base64url）；
  - 带密码的 URL（`scheme://user:pass@`）。
- 命中的内容替换为 `[REDACTED:<类型>:<原文 SHA-256 前 8 位>]`。
- 管理后台明确提示：正则规则无法保证覆盖所有情况。

### 6.4 检索

- 对 `is_new` 的用户输入片段和模型最终回复片段抽取纯文本，写入 `request_texts` 表，建 `pg_trgm` 索引，支持中文子串搜索。
- 可以按员工、时间段、客户端类型、模型、关键词组合筛选。

## 7. 权限、审计与告知

| 角色 | 如何确定 | 权限 |
|---|---|---|
| 超级管理员 | 设置里配置的飞书用户列表 | 全部数据和操作，可看审计 |
| 部门负责人 | 飞书部门的负责人字段，范围含下级部门 | 本部门员工的用量、原文、日报，只读 |
| 员工 | 默认 | 自己的 Key、用量、额度、原文、日报 |

**审计表**只能追加，不能修改。记录以下操作：
- 查看他人原文：谁、看了谁、哪条请求、时间、IP；
- 所有写操作：添加或停用账号、绑定和解绑、重置或撤销 Key、更换 IP 组、修改设置、角色变更。

**告知**：员工首次登录时必须点"我已知晓"，时间写入 `employees.notice_ack_at`。未确认的员工不能生成 Key。建议公司把这条同步写进 IT 使用制度。

## 8. 页面

### 员工自助页

- 飞书扫码登录。
- 首次登录：告知确认。
- 我的 Key：只完整显示一次，之后可以重置。
- 一键配置：
  - Codex `config.toml` 片段（推荐），包含 `model_provider`、`base_url`、`wire_api = "responses"`，并关闭 WebSocket；
  - 第三方客户端的填写说明，标注风险提示。
- 我的用量：今天、7 天、30 天，按模型和客户端拆分。
- 账号额度与状态。
- 我的日报。

### 管理后台

部门负责人看到同一套页面，但只读，范围限定在本部门。

- 总览：用量趋势、活跃人数、账号健康汇总、未处理告警。
- 员工：绑定关系、Key 状态、用量。
- 账号：添加向导、状态、IP 组、设备档案、健康信号。
- IP 组。
- 日志检索和对话还原视图（打开即审计）。
- 日报。
- 审计记录（仅超级管理员）。
- 设置：默认并发数、排队超时、Codex 版本模板、保留天数、脱敏规则、客户端识别规则、管理员名单、日报模型端点。

## 9. AI 日报

- **生成时间**：每天 08:00（北京时间）生成前一天的日报，09:00 通过飞书推送。
- **输入**：该员工前一天每个会话里 `is_new` 的用户输入和模型最终回复。工具输出截断到 500 字符，代码块截断到 40 行。
- **生成过程**：
  1. 每个会话先生成一段小结（2–4 句）；
  2. 再汇总成当天日报，包括：做了哪些项目和任务、遇到的问题和卡点。
  3. 数字（活跃时段、请求数、token 数、客户端分布）全部由 SQL 计算后填入模板，不让模型生成。
- **模型**：可配置的 OpenAI 兼容端点，默认用单独的 OpenAI API Key 调小模型。**不使用员工的 Pro 订阅**。
- **推送**：
  - 员工收到自己的日报；
  - 部门负责人收到本部门的汇总卡片，可展开看每个人。
  - 没有活动的员工不生成日报，汇总卡片里列为"无使用"。
- **安全**：日志内容只作为待总结的文本传给模型，不附带任何工具或动作能力。

## 10. 数据模型（主要表）

- `employees`：`id`, `feishu_open_id`, `feishu_union_id`, `name`, `email`, `status`, `is_super_admin`, `notice_ack_at`, `created_at`
- `departments`：`id`, `feishu_dept_id`, `parent_id`, `name`, `leader_employee_ids[]`；`employee_departments`：员工与部门的多对多关系
- `ip_groups`：`id`, `name`, `proxy_url_enc`, `capacity`, `expected_egress_ip`, `last_egress_ip`, `last_checked_at`
- `upstream_accounts`：`id`, `cpa_auth_id`, `email`, `plan_type`, `owner_type`, `employee_id UNIQUE NULL`, `ip_group_id`, `status`, `status_detail`, `rate_limited_until`, `max_concurrency NULL`, `device_profile JSONB`, `created_at`
- `api_keys`：`id`, `employee_id`, `key_hash UNIQUE`, `key_prefix`, `created_at`, `revoked_at`, `last_used_at`
- `sessions`：`id`, `employee_id`, `account_id`, `session_key`, `client_kind`, `first_at`, `last_at`, `request_count`
- `requests`（按月分区）：`id`（UUIDv7）, `employee_id`, `account_id`, `api_key_id`, `session_id`, `client_kind`, `user_agent`, `endpoint`, `model_requested`, `model_responded`, `status_code`, `error_type`, `queued_ms`, `ttft_ms`, `duration_ms`, `tokens_input`, `tokens_cached`, `tokens_output`, `tokens_reasoning`, `started_at`
- `request_segments`：`request_id`, `direction`（req/resp）, `seq`, `blob_hash`, `is_new`
- `blobs`：`hash PK`, `kind`, `data_zstd`, `raw_size`, `last_referenced_at`
- `request_texts`：`request_id`, `role`, `text`（带 `pg_trgm` 索引）
- `account_signals`：`id`, `account_id`, `kind`, `detail JSONB`, `at`, `resolved_at`
- `quota_snapshots`：`account_id`, `at`, `primary_used_pct`, `primary_reset_at`, `secondary_used_pct`, `secondary_reset_at`
- `daily_reports`：`id`, `employee_id`, `date`, `stats JSONB`, `summary_md`, `model`, `generated_at`，`(employee_id, date)` 唯一
- `audit_logs`：`id`, `actor_employee_id`, `action`, `target_type`, `target_id`, `detail JSONB`, `ip`, `at`
- `settings`：`key PK`, `value JSONB`

## 11. 实现阶段需要先实测验证的假设

用一个真实账号在预发环境逐条验证。任何一条不成立，都先回到设计层调整，再继续实现。

1. 在 `force-model-prefix: true` 下，`acct-x/<model>` 只会选中 prefix 为 `acct-x` 的账号；该账号冷却中或失效时，**请求失败而不是回落到其他账号**。
2. `disable-codex-cloaking: true` 时，OAuth 类型的 codex 账号会透传客户端的 UA 和 Originator（源码 `codex_executor_request.go` 显示如此）。
3. `passthrough-headers` 能把上游的 Codex 额度响应头回传给网关。
4. CPA 会去掉 `stream_options`。确认 chat completions 流式响应的最后一个 chunk 里是否仍带 usage；如果不带，改从 CPA 的 usage 队列（`/v0/management/usage-queue`）按 `request_id` 回填。
5. 当前 Codex CLI（0.157.x）自定义 provider 关闭 WebSocket 的配置项名称。
6. 通过管理 API 完成 Codex OAuth 登录（`codex-auth-url` 加回调 URL 转交）的具体调用方式。
7. token 刷新请求是否也走账号自己的 `proxy_url`。

## 12. 技术栈

- **后端**：Go；路由用 chi；数据库用 pgx + sqlc，迁移用 goose；zstd 用 klauspost/compress；日志用 slog。
- **前端**：React + Vite + TypeScript；组件库 shadcn/ui；图表 Recharts。前端产物通过 `go:embed` 打进网关二进制。
- **飞书**：
  - 网页应用 OAuth 登录（authen v1）；
  - 通讯录 v3：部门、负责人、用户状态，每小时增量同步；
  - IM v1：发送卡片消息。
- **敏感配置**：代理地址用 AES-GCM 加密存储，主密钥来自环境变量。CPA 管理密钥、内部 key、飞书 App Secret 都通过环境变量注入。
- **会话**：httpOnly + SameSite=Lax 的 Cookie；写操作校验 CSRF token。

## 13. 分期

| 里程碑 | 内容 | 验收标准 |
|---|---|---|
| M1 数据面 | Compose 编排、CPA 配置、鉴权、绑定、并发闸、指纹处理、转发、请求元数据和去重原文落库；用 CLI 命令添加账号、签发 Key；完成 §11 的实测 | 用真实 Codex CLI 经网关完成多轮编码任务；并发第 5 个请求会排队；数据库里能还原出完整对话 |
| M2 身份与后台 | 飞书登录、组织同步、告知确认、员工自助页、账号添加向导、IP 组、权限范围、审计 | 管理员能在页面上完成"加账号 → 绑员工 → 员工自助拿 Key 和配置"；部门负责人只能看到本部门 |
| M3 看板与健康 | 用量看板、日志检索和对话还原、账号健康状态和信号、额度快照、出口 IP 检查、飞书告警、Codex 版本提醒 | 人为制造的模型不一致、出口 IP 变更都能触发告警 |
| M4 日报与清理 | AI 日报生成和推送、180 天原文清理、spool 回放 | 连续 3 天自动推送；清理任务后能还原的原文只剩 180 天内的 |

## 14. 测试

- **单元测试**：
  - Key 生成和校验；
  - 信号量：FIFO、超时、取消时释放，用 `-race` 跑；
  - 客户端识别和请求头处理，用 golden 文件；
  - 片段拆分、去重、还原的往返一致性；
  - 脱敏规则，表驱动；
  - SSE 解析：Responses 和 chat completions 两种，用真实抓包 fixture；
  - 权限范围计算。
- **集成测试**：
  - 用 `httptest` 起一个假 CPA：记录收到的请求头和 model，回放 fixture SSE，并统计最大并发数；
  - 网关连真实 PostgreSQL（testcontainers）跑端到端测试；
  - 断言：20 个并发请求打同一个账号时，假 CPA 观测到的最大并发数为 4；非 Codex 客户端发出的请求头等于设备档案。
- **预发验证**：真实 CPA + 一个真实账号，逐条跑完 §11 的清单，结果写进 `docs/verification/`。

## 15. 风险

| 风险 | 缓解 |
|---|---|
| OpenAI 认定订阅转 API 违规，账号被标记或降级 | 一人一号；固定出口 IP；推荐官方 Codex 客户端；健康监测可以早发现；接受"灰色地带"这一剩余风险 |
| 日志含源码和密钥，泄露面大 | 脱敏、权限按部门划分、查看留审计、180 天清理、磁盘加密（云盘自带） |
| CPA 快速迭代导致行为变化 | 固定 CPA 镜像版本；升级前在预发重跑 §11 验证清单和集成测试 |
| 单机故障 | 每天 `pg_dump` 并备份 CPA 凭证目录到对象存储，恢复流程写成文档 |
| 员工对被记录的反感 | 首次登录告知、员工可以看到自己的全部数据、写入 IT 制度 |
