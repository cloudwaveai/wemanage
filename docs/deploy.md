# 部署指南

## 前置

- 一台海外服务器（Docker + Docker Compose v2）。
- 一个解析到该服务器的域名（Caddy 自动签发 TLS）。
- 若干海外静态出口代理（分组固定 IP，每组 5–10 个账号），格式如 `http://user:pass@host:port` 或 `socks5://user:pass@host:port`。
- 一个飞书自建应用（App ID / App Secret），开通：网页登录、通讯录只读、发消息。
- 一个用于 AI 日报的 OpenAI 兼容端点与 **独立** API Key（勿用员工订阅）。
- 每位员工一个 ChatGPT 订阅账号（用于后续在后台逐个添加）。

## 一次性部署

```bash
cd deploy
cp .env.example .env          # init.sh 也会自动生成密钥
./init.sh                     # 生成密钥、渲染 cpa/config.yaml、构建并启动
# 按提示补全 .env 里的 GATEWAY_DOMAIN / PUBLIC_BASE_URL / FEISHU_* / REPORT_LLM_* / BOOTSTRAP_SUPER_ADMINS
docker compose up -d          # 重新加载补全后的 .env

# 应用数据库迁移 + 建超管种子
docker compose run --rm gateway migrate
docker compose run --rm gateway admin bootstrap
```

完成后访问 `https://<你的域名>/`，用飞书扫码登录。`BOOTSTRAP_SUPER_ADMINS` 里的 open_id 会成为超级管理员。

## 飞书应用配置

1. 开发者后台创建企业自建应用，拿到 App ID / App Secret 填入 `.env`。
2. 安全设置里，把重定向 URL 加为 `https://<域名>/api/auth/feishu/callback`。
3. 权限：`contact:department.base:readonly`、`contact:user.base:readonly`（或等价读通讯录权限）、`im:message`（发消息）、网页登录（authen）。
4. 发布版本并让管理员在企业内安装。

## 添加员工账号（管理后台）

1. 先在"IP 组"里建好分组，填代理地址与容量。
2. "账号 → 添加"：选 IP 组 → 打开 OAuth 链接在浏览器登录该员工的 ChatGPT → 回调页打不开属正常，复制地址栏完整 URL 粘回后台 → 选择绑定的员工。
3. 或用"导入凭证 JSON"（如已有 `~/.codex/auth.json`）。

## 首批验证

上线前请完成 [verification/README.md](verification/README.md) 的 A/B 两类核实。

## 升级 CPA

CPA 迭代很快。升级只需改 `.env` 的 `CPA_IMAGE` 到新版本 tag，然后：

```bash
docker compose pull cpa && docker compose up -d cpa
```

升级前建议在预发重跑验证清单的 A 类与集成测试。**生产不要用 `latest`，固定到具体版本。**
