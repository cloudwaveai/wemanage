# 防 Key 外泄 / 倒卖：设备登记与风险控制

- 日期：2026-09-28
- 状态：已确认（严格程度 = 标准；非 Codex 客户端 = 禁止）
- 关联：主设计文档 `2026-09-27-ai-seat-gateway-design.md`

## 1. 目标与边界

员工可能把自己的 `sk-emp-…` 分享或卖给外人。外人多人使用同一个上游账号，会引发异常并发、异地访问，账号容易被 OpenAI 降智或风控；公司还要替外人付费，数据也可能外泄。

**目标**：尽早发现、准确定位到人、让倒卖变难且不划算。

**不追求**：百分之百阻止。员工可以在自己电脑上架中转，把买家的请求伪装成自己的 Codex 发出，此时设备和 IP 都是他本人的。这种情况只能靠用量异常、全量留痕（可以追到人）和公司制度来约束。

## 2. 可用信号

| 信号 | 来源 | 用途 |
|---|---|---|
| Codex 安装 ID | 请求体 `client_metadata.x-codex-installation-id` | 最强的设备标识 |
| 客户端类型 | `User-Agent` / `Originator`（已有识别规则） | 禁止非 Codex 客户端 |
| 客户端 IP | Caddy 写入的 `X-Forwarded-For`（只信任来自内网的请求） | 网络变化、同时多地使用 |
| 省份、运营商、是否机房 | 离线 IP 库 ip2region（可选） | 机房 IP、异地访问 |
| 活跃设备数、用量、活跃小时数 | 数据库 | 行为异常 |

**设备键**：有安装 ID 时为 `inst:<安装ID>`；没有时为 `ua:<sha256(UA|Originator) 前 16 位>`。

**网络键**：IPv4 取 /24 记录，IPv6 取 /48；判断"不同网络"时 IPv4 用 /16 比较（办公网 NAT 池、移动网络换 IP 通常在同一个 /16 内，这样可以减少误报）。

## 3. 数据面同步检查（只查内存缓存，不拖慢请求）

按顺序检查，任一命中即拒绝，返回 OpenAI 错误格式：

1. 员工被冻结（`employees.frozen_at` 非空）→ 403 `account_frozen`
2. 非 Codex 客户端且设置 `allow_non_codex=false`（默认）→ 403 `client_not_allowed`；开启 `require_codex_installation_id` 后，不带安装 ID 的"Codex"请求同样拒绝
3. 设备状态为 `blocked` → 403 `device_blocked`
4. 设备为 `pending`，并且满足下面任一条件 → 403 `device_unconfirmed`（错误信息里附门户链接）：
   - 严格模式；
   - 已超过确认期限（默认 24 小时）。
5. 设备第一次出现 → 登记为 `pending`；严格模式下拒绝，标准和宽松模式下放行。

检查通过后，把一条观测记录（员工、设备、IP、时间、客户端类型、设备是否已信任）异步交给风险引擎。

## 4. 风险引擎规则（后台）

| 规则 | 严重度 | 标准模式处置 |
|---|---|---|
| `new_device` 新设备 | warn | 飞书通知员工，请他去门户确认 |
| `non_codex_blocked` 非 Codex 客户端被拦截 | warn | 通知员工（同一设备每天最多一次） |
| `datacenter_ip` 机房 IP | warn | 通知员工和管理员（员工挂 VPN 也可能命中，所以不冻结） |
| `too_many_devices` 7 天内活跃设备超过 3 台 | warn | 通知管理员 |
| `concurrent_untrusted` 窗口内（默认 10 分钟）有未信任设备，且与另一台设备分处不同 /16 网络；该设备已存在超过 30 分钟 | high | **冻结员工**，通知管理员和员工 |
| `multi_network` 窗口内出现 3 个以上 /16 网络 | high | **冻结员工** |
| `device_denied` 员工在门户点了"不是我" | high | 作废 Key，拉黑设备，通知管理员 |
| `volume_spike` 当天请求数超过本人 14 天中位数的 3 倍，且不少于 200 次 | warn | 通知管理员（每日任务） |
| `sleepless` 一天中有 20 个或更多小时有请求 | warn | 通知管理员（每日任务） |

**宽松模式**：所有规则只通知，不拦截、不冻结。
**严格模式**：新设备当场拦截；每人最多 2 台已信任设备。

**冻结是员工级别的**：冻结期间不能用任何 Key，也不能自己重新生成 Key，**只有管理员能解冻**（否则真在倒卖的人自己就能解开）。

## 5. 设备生命周期

```
新设备 ──► pending ──员工确认──► trusted
              │  └─员工点"不是我"──► blocked（同时作废 Key、高风险事件）
              └─超过确认期限──► expired（拦截，员工确认后变 trusted）
管理员可以随时 block / unblock / trust
```

## 6. 数据模型（迁移 00003）

- `employees`：新增 `frozen_at`、`frozen_reason`、`notice_ack_version`
- `requests`：新增 `client_ip`、`device_id`
- `client_devices`：`(employee_id, device_key)` 唯一；记录 kind、label、首次/最近出现时间与 IP、请求数、status、确认时间和确认人
- `client_networks`：`(employee_id, net_key)` 唯一；记录 IP 样本、国家、省份、城市、运营商、是否机房、首次/最近出现时间、请求数
- `risk_events`：employee、rule、severity、detail、device_id、network_id、时间、notified_at、resolved_at、resolved_by

## 7. 告知版本

使用告知改为带版本号，默认版本 2，新增一句："为保障账号安全，会记录你使用的设备和来源 IP。"员工确认过的版本低于当前版本时，需要重新确认才能继续使用。迁移时把已确认过旧告知的员工标记为版本 1。

## 8. 接口

**员工**：
- `GET /api/me/devices`
- `POST /api/me/devices/{id}/confirm`
- `POST /api/me/devices/{id}/deny`
- `GET /api/me` 增加 `frozen`、`frozen_reason`、`pending_devices`、`notice_version`

**管理员**：
- `GET /api/admin/risk/events`
- `POST /api/admin/risk/events/{id}/resolve`
- `GET /api/admin/employees/{id}/devices`
- `POST /api/admin/devices/{id}/{trust|block|unblock}`
- `POST /api/admin/employees/{id}/unfreeze`
- `POST /api/admin/employees/{id}/freeze`（管理员手动冻结，附原因）

所有写操作都记审计日志。

## 9. 配置（设置页）

- `risk_mode`：`lax` / `standard` / `strict`，默认 `standard`
- `allow_non_codex`：默认 `false`（即禁止非 Codex 客户端）
- `device_confirm_hours`：默认 24
- `concurrency_window_minutes`：默认 10
- `new_device_grace_minutes`：默认 30（范围 1–1440）
- `max_active_devices`：默认 3
- `require_codex_installation_id`：默认 `false`。先观察真实流量，确认所有 Codex 请求都带安装 ID 后再打开，防止有人伪造 UA 冒充 Codex。

**环境变量**：
- `GEOIP_DB`：ip2region 数据文件路径，可选；不配置则跳过地理位置和机房判断。
- `TRUST_PROXY_HEADERS`：默认 `true`。

## 10. 测试要点

- 客户端 IP 提取：只在请求来自内网时信任 `X-Forwarded-For`，取最右侧一个。
- 设备键：有安装 ID 时优先用它；没有时退回 UA。
- 网络键：/24 和 /16 的计算。
- 同时使用的判定：
  - 已信任设备不触发；
  - 新设备在 30 分钟宽限期内不触发；
  - 3 个以上网络同时出现时，不管设备是否信任都触发。
- 数据面：以下情况各返回对应错误码——非 Codex 客户端 403、被冻结 403、超过确认期限的 pending 设备 403、严格模式下的新设备 403；标准模式下新设备放行并登记为 pending。
- 门户：确认设备、点"不是我"会作废 Key 并记事件、冻结期间不能重新生成 Key、需要重新确认告知。
- 定时任务：确认期限到期、用量异常。

## 11. 实现说明（2026-09-28 落地）

- 代码位置：`internal/risk`（身份识别、同步检查、并发窗口、风险引擎）、`internal/store/risk.go`、`internal/web/api_risk.go`，数据面接入在 `internal/proxy/handler.go` 的 "2b" 段。
- **设备缓存**：数据面按 (员工, 设备键) 缓存设备状态 15 秒；员工确认、管理员操作、到期任务都会主动清掉该员工的缓存，所以状态变化立即生效。员工级冻结走已有的 Key 缓存（3 秒）。
- **失败放行**：设备登记查询数据库出错时记日志并放行，避免数据库抖动让所有人都用不了；客户端类型和冻结检查不依赖这一步。
- **异步引擎**：观测记录进 4096 长度的队列，由单独的协程处理；队列满时丢弃并打日志（检测尽力而为，服务优先）。
- **去重**：`non_codex_blocked`、`too_many_devices` 同一员工 24 小时最多一次（内存 + 数据库双重判断，重启也不会重复推送）；高风险规则同一员工 1 小时最多一次（只用内存）。管理员解冻时清空该员工的并发窗口和去重记录，旧记录不会让他立刻再被冻结。
- **"不是我"**：拉黑设备 + 作废员工当前 Key + 记高风险事件 + 通知管理员；不冻结员工（这是员工主动上报，走正常路径），员工可以立即重新生成 Key。
- **管理员解除拉黑**：设备回到 `pending`，需要员工重新确认。
- **严格模式**：新设备当场拦截（仍登记为 pending 并通知员工确认）；员工最多确认 2 台设备，超出返回 409。
- **定时任务**：每小时把超期的 pending 设备置为 expired 并通知员工（宽松模式跳过）；每天 08:10 统计前一天的 `volume_spike`、`sleepless`，给管理员推一条汇总。
- **通知对象**：员工消息发给本人的飞书 open_id；管理员消息发给数据库里所有在职超级管理员。
- **第三方客户端配置页**：`/api/me/config-snippets` 的 `third_party.allowed` 反映当前是否放开，禁用时提示文案改为"会被拦截"。
- **告知版本**：`settings.Put` 检测到文案变化时自动给 `notice_version` 加 1；数据面和"生成 Key"都要求 `notice_ack_version >= notice_version`。

## 12. 补充：公司网络限制与内容审核（2026-09-28）

参考另一家公司的中转方案后补充了两项：

- **公司网络限制**：设置 `office_only` + `office_networks`（CIDR 或单个 IP）。开启后数据面在客户端类型检查之前先查来源 IP，不在公司网段且员工没有 `remote_allowed`（迁移 00004）就返回 403 `network_not_allowed`，并记 `outside_office` 事件（员工每天最多提醒一次）。这是防 Key 外泄最硬的一道：泄露的 Key 出了公司网络就没用；远程办公的人单独放开，仍受设备、并发规则约束。
- **内容审核**：设置 `content_audit_mode`（`off`/`warn`/`block`，默认 `warn`）+ `content_audit_rules`（不区分大小写的正则）。只检查请求里最新一条用户消息（`internal/audit`），命中记 `content_flagged` 事件（规则名、前后约 40 字片段、是否拦截），同一员工同一规则一小时提醒管理员一次；`block` 模式返回 403 `content_blocked`。默认规则针对 OpenAI 会封号的网络滥用，力求少误报。
- **检查顺序**：冻结 → 告知版本 → 账号状态 → 公司网络 → 客户端类型 → 内容审核（记录结果）→ 设备 → 内容拦截 → 并发排队。
- **WebSocket**：对照 Codex 源码确认不需要支持（自定义服务默认 HTTP，收到 426 会退回 HTTP），员工配置片段显式写 `supports_websockets = false`。
- **团队用量**：`GET /api/team/usage` 给部门负责人只读查看本部门成员的用量、账号状态和额度。
