# AI 订阅网关（ai-seat-gateway）

给公司员工统一分发 AI 订阅（当前为 ChatGPT/Codex）的自建网关：**一人一号、全量日志、按账号限并发、指纹一致、管理员管控**。自研网关在前、原版 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)（CPA）在后。

```
员工客户端 ──HTTPS──▶ Caddy ──▶ gateway ──内网──▶ CPA ──固定出口IP──▶ chatgpt.com
   (Codex/Cursor/…)            (身份/并发/指纹/日志)   (OAuth 刷新/上游协议)
```

## 它做什么

- **一人一号**：员工拿到 `sk-emp-…` Key，固定路由到本人专属账号，不做号池轮询。
- **全量日志**：请求与回复原文（按片段去重存储）、token、耗时；用量看板、原文检索、每日 AI 工作日报（推送飞书）。
- **限并发**：每账号默认最多 4 个在途请求，超出排队。
- **指纹一致**：官方 Codex 客户端透传真实请求头；其它客户端套用账号固定设备档案；每账号固定出口 IP。
- **管理与权限**：飞书登录；超管看全部、部门负责人看本部门；查看原文留审计；原文默认保留 180 天。
- **防 Key 外泄**：默认只放行官方 Codex 客户端；登记每个 Key 用过的设备和网络，新设备要员工本人确认；同一 Key 在不同网络同时使用会自动冻结，只有管理员能解冻（风险中心）。

## 文档

- 设计：[docs/superpowers/specs/2026-09-27-ai-seat-gateway-design.md](docs/superpowers/specs/2026-09-27-ai-seat-gateway-design.md)
- 实施计划：[docs/superpowers/plans/2026-09-27-ai-seat-gateway-v1.md](docs/superpowers/plans/2026-09-27-ai-seat-gateway-v1.md)
- 防 Key 外泄设计：[docs/superpowers/specs/2026-09-28-key-sharing-risk-design.md](docs/superpowers/specs/2026-09-28-key-sharing-risk-design.md)
- 部署：[docs/deploy.md](docs/deploy.md)　运维：[docs/operations.md](docs/operations.md)
- 员工指南：[docs/employee-guide.md](docs/employee-guide.md)
- 合规与告知：[docs/compliance-note.md](docs/compliance-note.md)
- 上线前验证清单：[docs/verification/README.md](docs/verification/README.md)

## 快速开始（开发）

```bash
make test-db-up      # 用本地 postgres:16-alpine 起测试库（:55432）
make test            # go test -race ./...
make build           # 编译 gateway 二进制
```

生产部署见 `deploy/`：`cp .env.example .env && ./init.sh`。

## 技术栈

Go 1.26（chi、pgx、goose）· PostgreSQL 16 · React + Vite（内嵌）· Caddy 2 · Docker Compose。

## 合规提醒

严格一人一号，不共享、不转售。"订阅经网关以 API 使用"对 OpenAI 属灰色地带，存在剩余风险；不接入 Claude 订阅（Anthropic 条款禁止）。记录员工对话需依《个人信息保护法》事先告知并取得同意——详见合规说明。
