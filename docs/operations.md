# 运维手册

## 日常

- **看健康**：管理后台"总览"看账号健康汇总与未处理告警。飞书会推送账号级告警（限额、需重新登录、疑似降智、出口 IP 变更）。
- **加/停员工**：员工由飞书通讯录每小时自动同步；飞书离职后其 Key 自动失效。手动停用见"员工"页。
- **加账号 / 换绑**：见 [deploy.md](deploy.md)。换绑、换 IP 组都会写审计。

## 账号状态含义

| 状态 | 处理 |
|---|---|
| 正常 | 无需处理 |
| 限额中 | 账号命中 5 小时/每周额度，到显示的恢复时间自动恢复 |
| 需重新登录 | 上游 token 失效：到"账号"页对该账号重新走一次 OAuth 登录 |
| 疑似降智 | 响应模型与请求不符，或推理量被封顶。考虑降低该账号并发、检查出口 IP，必要时联系 OpenAI |
| 已停用 | 管理员手动停用 |

## 更换出口 IP

到"IP 组"改代理地址。注意：**更换出口 IP 本身是风控信号**，非必要不换；每次更换都记审计。系统每 30 分钟自检各组实际出口 IP，代理商偷偷换 IP 会告警。

## 备份与恢复

**要备份的**：PostgreSQL 数据卷（`pgdata`）与 CPA 凭证目录（`deploy/cpa/auths`，含各账号 OAuth token）。

```bash
# 备份数据库
docker compose exec -T postgres pg_dump -U gateway gateway | gzip > backup-$(date +%F).sql.gz
# 备份 CPA 凭证
tar czf auths-$(date +%F).tgz -C deploy/cpa auths
```

建议每天定时备份并上传到对象存储，异地留存。

**恢复**：新机器上 `./init.sh` 起环境后，先恢复 `deploy/cpa/auths`，再 `gunzip -c backup.sql.gz | docker compose exec -T postgres psql -U gateway gateway`，最后 `docker compose up -d`。

## 数据保留

- 对话原文（片段）超过设置里的保留天数（默认 180）自动删除，每天 03:30 执行。
- 用量统计、日报、审计永久保留。
- 日志写库失败会先落到 `gateway_spool` 卷，数据库恢复后自动回放；若该卷持续增长，说明数据库有问题，需排查。

## 日报

每天 08:00（北京时间）生成前一日日报，09:00 推送飞书。用的是 `.env` 里配置的独立 OpenAI 兼容端点的小模型，**不消耗员工订阅**。可在"设置"里开关。

## 调整并发 / 超时

"设置"页可改默认并发（1–32）、排队超时（1–600 秒）、保留天数、Codex 版本模板、脱敏规则、客户端识别规则、管理员名单。也可对单个账号单独设并发上限。

## 排障

- **员工报 502**：CPA 不可达，`docker compose logs cpa`。
- **员工报 503 account_needs_login**：该账号 token 失效，重新登录。
- **额度不显示**：确认 CPA `passthrough-headers: true`，且上游确实返回了额度头（见验证清单 B2）。
- **看 CPA 明细**：临时把 `cpa/config.yaml` 的 `request-log` 设为 true 并重启 cpa（排障后记得关掉，避免重复留存明文）。
