package web

import (
	"fmt"
	"net/http"
	"strings"
)

// The way back to the official login. The install command rewrites Codex's
// config.toml behind CC Switch's back; when CC Switch next switches it saves
// that config into whichever provider it thought was current (usually
// "OpenAI Official"), so switching back in CC Switch brings the gateway back.
// `curl -fsSL <portal>/restore | sh` takes the gateway out of config.toml and
// out of any CC Switch provider it leaked into. It holds no key, so it is a
// plain public script.

// stripAiseatAwk drops what the install command wrote: the top-level
// model_provider = "aiseat", the [model_providers.aiseat] table, and a
// top-level model only the gateway serves (official Codex knows the gpt-*).
const stripAiseatAwk = `/^[ \t]*\[/ { intable = 1; skip = ($0 ~ /^[ \t]*\[model_providers\.aiseat[].]/) }
skip { next }
!intable && /^[ \t]*model_provider[ \t]*=[ \t]*"aiseat"/ { next }
!intable && /^[ \t]*model[ \t]*=/ && $0 !~ /"gpt-/ { next }
{ print }`

func (s *Server) handleRestoreScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasSuffix(r.URL.Path, ".ps1") {
		fmt.Fprint(w, restorePs)
		return
	}
	fmt.Fprint(w, restoreSh)
}

var restoreSh = `#!/bin/sh
# AI 订阅网关：把 Codex 切回官方登录（去掉网关配置，其它设置不动）
set -eu
DIR="${CODEX_HOME:-$HOME/.codex}"
CONFIG="$DIR/config.toml"
TS="$(date +%Y%m%d%H%M%S)"
TMP="$(mktemp "${TMPDIR:-/tmp}/aiseat.XXXXXX")"
trap 'rm -f "$TMP"' EXIT
strip() {
  awk '
` + stripAiseatAwk + `
  '
}

if [ -f "$CONFIG" ] && grep -q 'model_providers\.aiseat\|^[ 	]*model_provider[ 	]*=[ 	]*"aiseat"' "$CONFIG"; then
  cp "$CONFIG" "$CONFIG.bak-$TS"
  echo "已备份当前配置：$CONFIG.bak-$TS"
  strip < "$CONFIG" > "$TMP"
  cat "$TMP" > "$CONFIG"
  echo "✓ 已从 $CONFIG 去掉网关配置"
else
  echo "✓ $CONFIG 里没有网关配置"
fi

# CC Switch 切换时会把当时的配置存进它的供应商，网关配置可能已经存进去了。
DB="$HOME/.cc-switch/cc-switch.db"
if [ -f "$DB" ] && command -v sqlite3 >/dev/null 2>&1; then
  ROWS="$(sqlite3 "$DB" "SELECT rowid FROM providers WHERE app_type='codex' AND json_extract(settings_config,'\$.config') LIKE '%model_providers.aiseat%'" 2>/dev/null || true)"
  if [ -n "$ROWS" ]; then
    NAMES="$(sqlite3 "$DB" "SELECT group_concat(name,'、') FROM providers WHERE rowid IN ($(echo $ROWS | tr ' ' ','))")"
    if pgrep -x cc-switch >/dev/null 2>&1; then
      echo "！CC Switch 的「$NAMES」里也存了网关配置。请先退出 CC Switch（菜单栏图标 → 退出），再运行一次这条命令。"
      exit 1
    fi
    cp "$DB" "$DB.bak-$TS"
    for row in $ROWS; do
      sqlite3 "$DB" "SELECT json_extract(settings_config,'\$.config') FROM providers WHERE rowid=$row" | strip > "$TMP"
      sqlite3 "$DB" "UPDATE providers SET settings_config=json_set(settings_config,'\$.config',CAST(readfile('$TMP') AS TEXT)) WHERE rowid=$row"
    done
    echo "✓ 已从 CC Switch 的「$NAMES」里去掉网关配置（备份：$DB.bak-$TS）"
  fi
fi

AUTH="$DIR/auth.json"
if [ -f "$AUTH" ] && grep -q '"refresh_token"' "$AUTH"; then
  echo "完成。重新打开 Codex（桌面版要完全退出再打开）就是你自己的 ChatGPT 账号。"
else
  echo "完成。这台电脑还没登录过官方账号：运行 codex login 用你自己的 ChatGPT 账号登录。"
fi
echo "以后要再用公司网关，回门户「我的 Key」重新生成安装命令即可。"
`

// restorePs is restoreSh for Windows PowerShell; CC Switch's database is left
// to the user there (no sqlite3 on a stock Windows).
var restorePs = `# AI 订阅网关：把 Codex 切回官方登录（去掉网关配置，其它设置不动）
$ErrorActionPreference = 'Stop'
$dir = if ($env:CODEX_HOME) { $env:CODEX_HOME } else { Join-Path $HOME '.codex' }
$config = Join-Path $dir 'config.toml'
if ((Test-Path $config) -and (Select-String -Path $config -Pattern 'model_providers\.aiseat|^\s*model_provider\s*=\s*"aiseat"' -Quiet)) {
  $backup = "$config.bak-" + (Get-Date -Format 'yyyyMMddHHmmss')
  Copy-Item $config $backup
  Write-Host "已备份当前配置：$backup"
  $keep = New-Object System.Collections.Generic.List[string]
  $inTable = $false; $skip = $false
  foreach ($line in [System.IO.File]::ReadAllLines($config)) {
    if ($line -match '^\s*\[') { $inTable = $true; $skip = $line -match '^\s*\[model_providers\.aiseat[\].]' }
    if ($skip) { continue }
    if (-not $inTable -and $line -match '^\s*model_provider\s*=\s*"aiseat"') { continue }
    if (-not $inTable -and $line -match '^\s*model\s*=' -and $line -notmatch '"gpt-') { continue }
    $keep.Add($line)
  }
  [System.IO.File]::WriteAllText($config, ($keep -join "` + "`n" + `") + "` + "`n" + `", (New-Object System.Text.UTF8Encoding $false))
  Write-Host "✓ 已从 $config 去掉网关配置"
} else {
  Write-Host "✓ $config 里没有网关配置"
}
$db = Join-Path $HOME '.cc-switch\cc-switch.db'
if ((Test-Path $db) -and (Select-String -Path $db -Pattern 'model_providers.aiseat' -SimpleMatch -Quiet)) {
  Write-Host -ForegroundColor Yellow '！CC Switch 的供应商里也存了网关配置：打开 CC Switch → Codex，编辑「OpenAI Official」（或你平时用的那个），删掉 model_provider = "aiseat" 这一行和 [model_providers.aiseat] 整段，保存。'
}
Write-Host '完成。重新打开 Codex 就是你自己的 ChatGPT 账号；没登录过的话运行 codex login。'
Write-Host '以后要再用公司网关，回门户「我的 Key」重新生成安装命令即可。'
`
