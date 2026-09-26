#!/usr/bin/env bash
# 首次部署引导：生成密钥、渲染配置、启动服务。
set -euo pipefail
cd "$(dirname "$0")"

need() { command -v "$1" >/dev/null 2>&1 || { echo "缺少命令：$1" >&2; exit 1; }; }
need docker
need openssl

if [ -f .env ]; then
  echo ".env 已存在，跳过密钥生成（如需重置请先手动备份删除）。"
else
  echo "生成 .env ..."
  cp .env.example .env
  PG_PW=$(openssl rand -hex 24)
  MASTER=$(openssl rand -base64 32)
  CPA_KEY="sk-internal-$(openssl rand -hex 24)"
  CPA_MGMT=$(openssl rand -hex 32)
  # 用 | 作分隔符，避免 base64 中的 / 干扰 sed
  sed -i.bak \
    -e "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${PG_PW}|" \
    -e "s|^MASTER_KEY=.*|MASTER_KEY=${MASTER}|" \
    -e "s|^CPA_API_KEY=.*|CPA_API_KEY=${CPA_KEY}|" \
    -e "s|^CPA_MANAGEMENT_KEY=.*|CPA_MANAGEMENT_KEY=${CPA_MGMT}|" \
    .env
  rm -f .env.bak
  echo "已写入随机密钥。请再补全 GATEWAY_DOMAIN / PUBLIC_BASE_URL / FEISHU_* / REPORT_LLM_* / BOOTSTRAP_SUPER_ADMINS。"
fi

# 渲染 CPA 配置
set -a; . ./.env; set +a
mkdir -p cpa/auths
sed -e "s|__CPA_INTERNAL_KEY__|${CPA_API_KEY}|" \
    -e "s|__CPA_MANAGEMENT_KEY__|${CPA_MANAGEMENT_KEY}|" \
    cpa/config.yaml.tmpl > cpa/config.yaml
echo "已渲染 cpa/config.yaml"

echo "构建并启动 ..."
docker compose up -d --build

echo
echo "等待数据库就绪后应用迁移与超管种子："
echo "  docker compose run --rm gateway migrate"
echo "  docker compose run --rm gateway admin bootstrap"
echo
echo "完成。访问 https://${GATEWAY_DOMAIN}/ ，用飞书登录。"
