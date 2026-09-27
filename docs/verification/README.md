# 上线前验证清单

> **已自动覆盖**：数据面「网关↔CPA」契约由 `internal/proxy` 的集成测试用假 CPA 上游验证（前缀路由、并发峰值=4、错误映射、限额反应、模型不一致信号、流式透传）；`serve` 二进制已手工冒烟通过（healthz、登录、告知门禁发 Key、`/v1` 401、SPA 渲染）。全部包 `go test -race` 通过。
>
> **下面这份清单**是用**真实 ChatGPT 账号**才能核实的部分，须在预发环境执行——本仓库未在开发沙箱中运行真实 CPA/真实账号。

设计文档 §11 列出了必须用真实环境核实的假设。分两类：

- **A 类（可离线验证）**：用源码构建的 CPA + 本地模拟上游即可跑通，见 `test/e2e`（`go test -tags e2e ./test/e2e/`）。
- **B 类（需真实账号/联网）**：必须用一个真实 ChatGPT 账号在预发环境人工核实，逐条记录结果。

## A 类：离线端到端（test/e2e）

| # | 假设 | 断言 |
|---|---|---|
| A1 | `force-model-prefix: true` + 前缀路由 | 请求 `acct-1/gpt-5.1-codex` 命中账号 1；账号 1 停用时**请求失败**而非改走账号 2 |
| A2 | `disable-codex-cloaking: true` 透传 UA/Originator | CPA 向模拟上游发出的 UA == 网关设置的 UA（不是 CPA 写死常量） |
| A3 | `passthrough-headers: true` 回传额度头 | 上游返回的 `X-Codex-Primary-Used-Percent` 出现在网关回给客户端的响应头 |
| A4 | chat 流式最后一块是否带 usage | 记录实际行为，决定是否需要回填 |

运行：

```bash
# 需要能构建 CPA（scratch 里已 clone，或设 CPA_SRC 指向仓库）
go test -tags e2e ./test/e2e/ -v
```

CPA 无法构建时该测试会 `t.Skip`，此时 A 类必须在预发用真实 CPA 镜像手工核实。

## B 类：预发人工核实（需真实账号）

逐条执行并把结果记到本目录下 `results-YYYYMMDD.md`：

- [ ] **B1 OAuth 登录**：管理后台"添加账号"能走通 `codex-auth-url` → 浏览器登录 → 粘贴回调 URL → CPA 完成 token 交换，账号出现在列表。
- [ ] **B2 真实额度头**：真实账号跑几次请求后，`/api/me/quota` 能显示 5 小时 / 每周使用比例（确认 CPA 确实透传了 `X-Codex-*-Used-Percent`）。
- [ ] **B3 token 刷新走代理**：给账号配一个只在特定出口可用的代理，确认 CPA 刷新 token 时也走该 `proxy_url`（抓包或看代理日志）。
- [ ] **B4 Codex 关 WebSocket 配置项**：确认当前 Codex CLI（0.157.x）自定义 provider 关闭 WebSocket 的配置键名，并写进员工配置片段。
- [ ] **B5 真实并发**：用真实账号并发 6 个请求，确认网关只放行 4 个、其余排队，且上游未收到 >4 并发。
- [ ] **B6 指纹自洽**：抓一次真实 Codex 请求，确认经网关后 UA / Version / Session-Id / Originator 一致且无 `X-Forwarded-*`。

### 防 Key 外泄（风险控制）

- [ ] **B7 安装 ID 存在**：用当前版本 Codex 发几次请求，到"我的设备"看设备类型是否为"Codex 安装"（即请求体里有 `client_metadata.x-codex-installation-id`）。如果显示"按客户端标识识别"，说明字段名变了或没发，**不要**打开"要求 Codex 安装 ID"，并回头核对字段。
- [ ] **B8 真实 IP**：从办公网和手机热点各发一次请求，确认"我的设备 → 网络"里是两个不同的公网 IP，而不是 Caddy 的内网地址。
- [ ] **B9 非 Codex 拦截**：用 curl 带员工 Key 调 `/v1/responses`，确认返回 403 `client_not_allowed`，员工收到飞书提醒，且 24 小时内重复调用不会重复提醒。
- [ ] **B10 新设备流程**：换一台电脑用同一个 Key，确认员工收到"新设备"消息；点"确认是我"后正常；在另一台测试机上点"不是我"后，旧 Key 立即失效、管理员收到通知。
- [ ] **B11 冻结与解冻**：一台未确认设备（新设备宽限期可临时调到 1 分钟）和一台已确认设备分别从不同运营商网络同时发请求，确认员工被冻结、双方收到通知、员工无法自助重新生成 Key；管理员解冻后恢复。
- [ ] **B12 误判摸底（可选）**：如果担心误冻结，可以先用"宽松"模式跑一周，只记录不冻结，确认风险中心里 `concurrent_untrusted` / `multi_network` 的命中都能解释后，再切回"标准"。

## 记录格式

每次核实新建 `results-<日期>.md`，逐条写：假设编号、命令/操作、观察到的结果、通过/不通过、备注。任一条不通过，回到设计层调整后再继续。
