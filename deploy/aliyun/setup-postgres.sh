#!/usr/bin/env bash
# 在国内云服务器（Alibaba Cloud Linux 3 / RHEL 8 系）上装 PostgreSQL 16，给海外网关存账本和日志。
#
# 用法：sudo bash setup-postgres.sh <海外网关公网IP> [nginx 对外端口，默认 15432]
#
# 连接路径：海外网关 → 本机 nginx（stream 四层转发，只放行网关 IP）→ 127.0.0.1:5432
# - PostgreSQL 只监听本机，不直接对外；SSL 是网关到数据库端到端的，nginx 只转发字节。
# - 软件从阿里云的 PGDG 镜像装，服务器不需要能访问境外。
# - 可以重复执行：已装的跳过；数据不动；密码只在第一次生成。
#   注意：pg_hba.conf、aiseat.conf 和 nginx 的 aiseat-pg.conf 每次都会按本脚本重写。
# - 宝塔 nginx 和系统 nginx 都支持：按正在运行的那个写配置，改前先 nginx -t，不通过就撤回。
# - 连接串写到 ~/.aiseat/database_url（仅你本人可读），脚本不会把密码打到屏幕上。
set -euo pipefail

GATEWAY_IP="${1:?用法: sudo bash setup-postgres.sh <海外网关公网IP> [nginx 对外端口]}"
PUBLIC_PORT="${2:-15432}"
[ "$(id -u)" = 0 ] || { echo "请用 sudo 运行"; exit 1; }
PUBLIC_HOST="$(curl -s -m 3 http://100.100.100.200/latest/meta-data/eipv4 || true)"
[ -n "$PUBLIC_HOST" ] || { echo "查不到本机公网 IP"; exit 1; }

PG=16
BIN=/usr/pgsql-$PG/bin
DATA=/var/lib/pgsql/$PG/data
MIRROR=https://mirrors.aliyun.com/postgresql/repos/yum
BT_NGINX=/www/server/nginx/sbin/nginx
BT_TCP_DIR=/www/server/panel/vhost/nginx/tcp
OWNER="${SUDO_USER:-root}"
OUT_DIR="$(getent passwd "$OWNER" | cut -d: -f6)/.aiseat"

# 转发配置要写给正在对外服务的那个 nginx：宝塔装的，或者系统 dnf 装的。
MASTER_PID="$(pgrep -o -f '^nginx: master' || true)"
[ -n "$MASTER_PID" ] || { echo "本机没有在运行的 nginx"; exit 1; }
NGINX="$(readlink -f "/proc/$MASTER_PID/exe")"
if [ "$NGINX" = "$BT_NGINX" ]; then
  STREAM_DIR=$BT_TCP_DIR
  grep -q "include $BT_TCP_DIR/\*.conf" /www/server/nginx/conf/nginx.conf ||
    { echo "宝塔 nginx.conf 的 stream 块里没有 include $BT_TCP_DIR/*.conf"; exit 1; }
  reload_nginx() { "$NGINX" -s reload; }
else
  STREAM_DIR=/etc/nginx/stream.d
  reload_nginx() { systemctl reload nginx; }
fi
case "$("$NGINX" -V 2>&1)" in *--with-stream*) ;; *) echo "$NGINX 没编译 stream 模块"; exit 1 ;; esac
if [ -n "$(ss -ltnH "sport = :$PUBLIC_PORT")" ] &&
  ! grep -qs "listen $PUBLIC_PORT " "$STREAM_DIR/aiseat-pg.conf"; then
  echo "端口 $PUBLIC_PORT 已被占用，换一个端口作为第二个参数"; exit 1
fi

install -d -m 700 -o "$OWNER" -g "$(id -gn "$OWNER")" "$OUT_DIR"
exec > >(tee "$OUT_DIR/setup.log") 2>&1
step() { printf '\n== %s\n' "$*"; }

step "1/7 安装 PostgreSQL $PG（阿里云 PGDG 镜像）"
cat >/etc/yum.repos.d/pgdg$PG-aliyun.repo <<EOF
[pgdg$PG-aliyun]
name=PostgreSQL $PG for RHEL 8 (mirrors.aliyun.com)
baseurl=$MIRROR/$PG/redhat/rhel-8-x86_64
enabled=1
gpgcheck=1
gpgkey=$MIRROR/keys/PGDG-RPM-GPG-KEY-RHEL
EOF
dnf -qy module disable postgresql >/dev/null 2>&1 || true
dnf -y install postgresql$PG-server postgresql$PG-contrib

step "2/7 初始化数据目录"
if [ -f "$DATA/PG_VERSION" ]; then
  echo "已存在 $DATA，跳过"
else
  PGSETUP_INITDB_OPTIONS="--encoding=UTF8 --locale=en_US.UTF-8 --auth-local=peer --auth-host=scram-sha-256" \
    "$BIN/postgresql-$PG-setup" initdb
fi

step "3/7 SSL 证书、参数、访问规则"
if [ ! -f "$DATA/server.key" ]; then
  openssl req -new -x509 -days 3650 -nodes -subj "/CN=aiseat-db" \
    -keyout "$DATA/server.key" -out "$DATA/server.crt" 2>/dev/null
  chown postgres:postgres "$DATA/server.key" "$DATA/server.crt"
  chmod 600 "$DATA/server.key"
fi
# 2 核 2G 的机器上还跑着别的服务，内存给得保守。
cat >"$DATA/aiseat.conf" <<'EOF'
listen_addresses = 'localhost'
port = 5432
max_connections = 40
shared_buffers = 128MB
effective_cache_size = 512MB
work_mem = 4MB
maintenance_work_mem = 64MB
password_encryption = 'scram-sha-256'
ssl = on
ssl_cert_file = 'server.crt'
ssl_key_file = 'server.key'
ssl_min_protocol_version = 'TLSv1.2'
timezone = 'UTC'
log_timezone = 'Asia/Shanghai'
EOF
grep -q "^include_if_exists = 'aiseat.conf'" "$DATA/postgresql.conf" ||
  echo "include_if_exists = 'aiseat.conf'" >>"$DATA/postgresql.conf"
# 外部连接都经 nginx 转发，到这里来源都是 127.0.0.1；按来源 IP 的放行在 nginx 上做。
cat >"$DATA/pg_hba.conf" <<'EOF'
# 由 setup-postgres.sh 生成，重新执行脚本会覆盖。
local    all      all                         peer
hostssl  gateway  gateway  127.0.0.1/32       scram-sha-256
EOF
chown postgres:postgres "$DATA/aiseat.conf" "$DATA/pg_hba.conf"

step "4/7 启动 PostgreSQL（开机自启）"
systemctl enable postgresql-$PG >/dev/null
systemctl restart postgresql-$PG
systemctl is-active postgresql-$PG

step "5/7 建库、账号、扩展"
psql_su() { (cd / && sudo -u postgres "$BIN/psql" -X -v ON_ERROR_STOP=1 -qtA "$@"); }
URL_FILE="$OUT_DIR/database_url"
url() { printf 'postgres://gateway:%s@%s:%s/gateway?sslmode=require&connect_timeout=10\n' "$1" "$PUBLIC_HOST" "$PUBLIC_PORT"; }
if [ -s "$URL_FILE" ]; then
  echo "已有 $URL_FILE，沿用原密码"
  PASS="$(sed -E 's#^postgres://gateway:([^@]+)@.*#\1#' "$URL_FILE")"
else
  PASS="$(openssl rand -hex 24)"
  if [ "$(psql_su -c "select count(*) from pg_roles where rolname='gateway'")" = 1 ]; then
    printf "ALTER ROLE gateway LOGIN PASSWORD '%s';\n" "$PASS" | psql_su
  else
    printf "CREATE ROLE gateway LOGIN PASSWORD '%s';\n" "$PASS" | psql_su
  fi
fi
(umask 077 && url "$PASS" >"$URL_FILE")
chown "$OWNER" "$URL_FILE"
echo "连接串已写入 $URL_FILE"
[ "$(psql_su -c "select count(*) from pg_database where datname='gateway'")" = 1 ] ||
  (cd / && sudo -u postgres "$BIN/createdb" -O gateway -E UTF8 gateway)
psql_su -d gateway -c "CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public"

step "6/7 nginx 四层转发 :$PUBLIC_PORT → 127.0.0.1:5432（$NGINX）"
CONF_BACKUP=""
if [ "$STREAM_DIR" != "$BT_TCP_DIR" ]; then
  # 系统 nginx 的 stream 是动态模块：装与当前 nginx 同版本的 nginx-mod-stream，不顺带升级 nginx。
  case "$("$NGINX" -V 2>&1)" in *--with-stream=dynamic*)
    rpm -q nginx-mod-stream >/dev/null 2>&1 ||
      dnf -y -x nginx install "nginx-mod-stream-$(rpm -q --qf '%{EPOCH}:%{VERSION}-%{RELEASE}' nginx)" ;;
  esac
  if ! grep -q "include $STREAM_DIR/\*.conf" /etc/nginx/nginx.conf; then
    CONF_BACKUP="/etc/nginx/nginx.conf.bak-aiseat-$(date +%Y%m%d%H%M%S)"
    cp -a /etc/nginx/nginx.conf "$CONF_BACKUP"
    printf '\n# 由 setup-postgres.sh 添加：四层（TCP）转发\nstream {\n    include %s/*.conf;\n}\n' "$STREAM_DIR" >>/etc/nginx/nginx.conf
  fi
  # 之前误写进宝塔目录的同名配置（宝塔 nginx 没在跑），清掉免得将来端口冲突。
  rm -f "$BT_TCP_DIR/aiseat-pg.conf"
fi
mkdir -p "$STREAM_DIR"
cat >"$STREAM_DIR/aiseat-pg.conf" <<EOF
# 由 setup-postgres.sh 生成：海外 AI 网关连数据库用，只放行网关 IP。
server {
    listen $PUBLIC_PORT so_keepalive=60s:10s:6;
    allow $GATEWAY_IP;
    allow 127.0.0.1;
    deny all;
    proxy_pass 127.0.0.1:5432;
    proxy_connect_timeout 5s;
    # 跨境长连接：闲置 1 小时才断（网关连接池 30 分钟就会自己换掉闲置连接）。
    proxy_timeout 1h;
    proxy_socket_keepalive on;
}
EOF
if ! "$NGINX" -t; then
  rm -f "$STREAM_DIR/aiseat-pg.conf"
  [ -z "$CONF_BACKUP" ] || cp -a "$CONF_BACKUP" /etc/nginx/nginx.conf
  echo "nginx 配置检查没通过，已撤回本脚本的改动，现有站点不受影响"; exit 1
fi
reload_nginx
sleep 1

step "7/7 自检（经 nginx 连一次）"
PGPASSWORD="$PASS" "$BIN/psql" -X -qtA "host=127.0.0.1 port=$PUBLIC_PORT dbname=gateway user=gateway sslmode=require" \
  -c "select 'login ok, ssl=' || s.ssl || ' ' || s.version || ', ' || v from pg_stat_ssl s, version() v where s.pid = pg_backend_pid()"
unset PASS
ss -ltn | awk 'NR==1 || $4 ~ /:(5432|'"$PUBLIC_PORT"')$/'

cat <<EOF

完成。还需要在阿里云控制台做一件事：
  ECS 安全组入方向放行 TCP $PUBLIC_PORT，授权对象只填 $GATEWAY_IP/32。
EOF
