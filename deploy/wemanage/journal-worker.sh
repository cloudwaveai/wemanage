#!/bin/sh
# Run under a single-instance flock on the database host.
set -eu
set -a
. "$HOME/.gateway-backup.env"
set +a
export PGPASSWORD="$DB_PASS"
export DATABASE_URL="postgres://gateway@127.0.0.1:15432/gateway?sslmode=require"
export WM_MODEL_URL="http://127.0.0.1:18320"
WM_MODEL_KEY=$(base64 -d "$HOME/wemanage/journal-worker/model-key.b64")
export WM_MODEL_KEY

ssh -o BatchMode=yes -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile="$HOME/.ssh/wemanage-dmit-known-hosts" \
  -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
  -i "$HOME/.ssh/wemanage-log-tunnel" -NT \
  -L 127.0.0.1:18320:172.19.0.2:8317 root@179.253.247.206 &
tunnel_pid=$!
trap 'kill "$tunnel_pid" 2>/dev/null || true' EXIT HUP INT TERM
n=0
while ! curl -sS -o /dev/null --max-time 3 "$WM_MODEL_URL/v1/models"; do
  n=$((n+1))
  if [ "$n" -ge 10 ]; then echo "model tunnel unavailable"; exit 1; fi
  sleep 1
done
"$HOME/wemanage/journal-worker/current"
