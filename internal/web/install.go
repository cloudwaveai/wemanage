package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"aiseat/internal/secure"

	"github.com/go-chi/chi/v5"
)

// One-command setup. The portal hands out a short-lived, single-use install
// code for the employee's key; `curl -fsSL <portal>/i/<code> | sh` (or
// `irm <portal>/i/<code>.ps1 | iex` on Windows) fetches a script that writes
// the gateway into Codex's config.toml, key included, and keeps the rest of
// the file. The key never appears in the command, so it stays out of shell
// history and chat logs; the code is dead once used.

const installTTL = 10 * time.Minute

type installEntry struct {
	top, table string // config.toml lines: top-level keys, and the provider table
	expires    time.Time
}

type installCodes struct {
	mu    sync.Mutex
	codes map[string]installEntry
}

func (c *installCodes) put(e installEntry) string {
	code := secure.RandomToken(16)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codes == nil {
		c.codes = map[string]installEntry{}
	}
	now := time.Now()
	for k, v := range c.codes {
		if now.After(v.expires) {
			delete(c.codes, k)
		}
	}
	c.codes[code] = e
	return code
}

// take returns the entry for code once; later calls, and expired codes, miss.
func (c *installCodes) take(code string) (installEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.codes[code]
	delete(c.codes, code)
	return e, ok && time.Now().Before(e.expires)
}

var modelIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,80}$`)

func (s *Server) handleInstallCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Key   string `json:"key"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	key := strings.TrimSpace(body.Key)
	emp := currentEmployee(r)
	prin, err := s.deps.Store.LookupKey(ctx, secure.HashAPIKey(key))
	if err != nil || prin.EmployeeID != emp.ID {
		writeErr(w, http.StatusBadRequest, "这不是你当前的 Key（可能已经重置过）。请到「我的 Key」重新生成。")
		return
	}
	cs := s.codexSetup(ctx, emp.ID)
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = cs.model
	}
	if !modelIDPattern.MatchString(model) || !cs.hasModel(model) {
		writeErr(w, http.StatusBadRequest, "选的模型不在你的账号里")
		return
	}
	top := fmt.Sprintf("model_provider = \"aiseat\"\nmodel = %q\n", model)
	table := fmt.Sprintf(`[model_providers.aiseat]
name = "AI 订阅网关"
base_url = %q
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = %q
%s%s
`, cs.base, key, cs.wsLine, cs.toolsLine)
	code := s.installs.put(installEntry{top: top, table: table, expires: time.Now().Add(installTTL)})
	portal := strings.TrimRight(s.deps.PublicBaseURL, "/")
	writeJSON(w, http.StatusOK, map[string]any{
		"expires_at": time.Now().Add(installTTL),
		"unix":       "curl -fsSL " + portal + "/i/" + code + " | sh",
		"windows":    "irm " + portal + "/i/" + code + ".ps1 | iex",
	})
}

// handleInstallScript serves the script for an install code (".ps1" for
// PowerShell). A used or expired code still gets a script, one that says
// so: curl -f would otherwise fail without a word.
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	ps := strings.HasSuffix(code, ".ps1")
	code = strings.TrimSuffix(code, ".ps1")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	portal := strings.TrimRight(s.deps.PublicBaseURL, "/")
	e, ok := s.installs.take(code)
	if !ok {
		msg := "这条安装命令已经失效（10 分钟内有效，只能用一次）。请回门户重新生成：" + portal + "/config"
		if ps {
			fmt.Fprintf(w, "Write-Host -ForegroundColor Red '%s'\n", msg)
		} else {
			fmt.Fprintf(w, "#!/bin/sh\necho '%s' >&2\nexit 1\n", msg)
		}
		return
	}
	host := portal
	if u, err := url.Parse(portal); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	if ps {
		fmt.Fprint(w, psScript(e, host))
	} else {
		fmt.Fprint(w, shScript(e, host))
	}
}

// shScript writes the gateway into ~/.codex/config.toml: a backup first,
// then our top-level model lines, the rest of the old file without its
// top-level model/model_provider and any earlier [model_providers.aiseat],
// and our provider table. The config is written in place (a symlinked
// config stays a symlink) and made private, since it holds the key. With a
// proxy in the environment, the gateway host is added to NO_PROXY in the
// shell's rc file.
func shScript(e installEntry, host string) string {
	return `#!/bin/sh
# AI 订阅网关：把公司网关写进 Codex 的配置（一次性安装脚本）
set -eu
DIR="${CODEX_HOME:-$HOME/.codex}"
CONFIG="$DIR/config.toml"
mkdir -p "$DIR"
NEW="$(mktemp "${TMPDIR:-/tmp}/aiseat.XXXXXX")"
trap 'rm -f "$NEW"' EXIT
cat > "$NEW" <<'AISEAT_TOP'
` + e.top + `AISEAT_TOP
if [ -f "$CONFIG" ]; then
  BACKUP="$CONFIG.bak-$(date +%Y%m%d%H%M%S)"
  cp "$CONFIG" "$BACKUP"
  echo "已备份原配置：$BACKUP"
  awk '
    /^[ \t]*\[/ { intable = 1; skip = ($0 ~ /^[ \t]*\[model_providers\.aiseat[].]/) }
    skip { next }
    !intable && /^[ \t]*(model|model_provider)[ \t]*=/ { next }
    { print }
  ' "$CONFIG" >> "$NEW"
fi
printf '\n' >> "$NEW"
cat >> "$NEW" <<'AISEAT_TABLE'
` + e.table + `AISEAT_TABLE
cat "$NEW" > "$CONFIG"
chmod 600 "$CONFIG"
echo "✓ 已写入 $CONFIG"

if [ -n "${HTTPS_PROXY:-}${https_proxy:-}${ALL_PROXY:-}${all_proxy:-}" ]; then
  case "${SHELL:-}" in
    */zsh) RC="$HOME/.zshrc" ;;
    */bash) if [ "$(uname)" = Darwin ]; then RC="$HOME/.bash_profile"; else RC="$HOME/.bashrc"; fi ;;
    *) RC="$HOME/.profile" ;;
  esac
  if ! grep -qs 'AI 订阅网关直连' "$RC"; then
    cat >> "$RC" <<'AISEAT_RC'

# AI 订阅网关直连：访问公司网关不走代理
export NO_PROXY="${NO_PROXY:+$NO_PROXY,}` + host + `"
export no_proxy="${no_proxy:+$no_proxy,}` + host + `"
AISEAT_RC
    echo "✓ 检测到终端代理，已在 $RC 里把 ` + host + ` 设为直连（新开的终端生效）"
  fi
fi

if command -v codex >/dev/null 2>&1; then
  echo "完成。重新打开 Codex 就会用公司网关。"
else
  echo "完成。还没装 Codex：npm install -g @openai/codex（或 brew install codex），装好后运行 codex 即可。"
fi
echo "提示：用 Clash 等代理软件的话，请把 ` + host + ` 设为直连（DIRECT）。"
`
}

// psScript is shScript for Windows PowerShell (5.1 and later), run with
// irm | iex: it must not call exit, which would close the window.
func psScript(e installEntry, host string) string {
	return `# AI 订阅网关：把公司网关写进 Codex 的配置（一次性安装脚本）
$ErrorActionPreference = 'Stop'
$dir = if ($env:CODEX_HOME) { $env:CODEX_HOME } else { Join-Path $HOME '.codex' }
$config = Join-Path $dir 'config.toml'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$top = @'
` + e.top + `'@
$table = @'
` + e.table + `'@
$rest = New-Object System.Collections.Generic.List[string]
if (Test-Path $config) {
  $backup = "$config.bak-" + (Get-Date -Format 'yyyyMMddHHmmss')
  Copy-Item $config $backup
  Write-Host "已备份原配置：$backup"
  $inTable = $false; $skip = $false
  foreach ($line in [System.IO.File]::ReadAllLines($config)) {
    if ($line -match '^\s*\[') { $inTable = $true; $skip = $line -match '^\s*\[model_providers\.aiseat[\].]' }
    if ($skip) { continue }
    if (-not $inTable -and $line -match '^\s*(model|model_provider)\s*=') { continue }
    $rest.Add($line)
  }
}
$text = $top.TrimEnd() + "` + "`n" + `" + ($rest -join "` + "`n" + `") + "` + "`n`n" + `" + $table.TrimEnd() + "` + "`n" + `"
[System.IO.File]::WriteAllText($config, $text, (New-Object System.Text.UTF8Encoding $false))
Write-Host "✓ 已写入 $config"

if ($env:HTTPS_PROXY -or $env:ALL_PROXY) {
  $np = [Environment]::GetEnvironmentVariable('NO_PROXY', 'User')
  if (-not $np -or $np -notlike '*` + host + `*') {
    $np = if ($np) { "$np,` + host + `" } else { '` + host + `' }
    [Environment]::SetEnvironmentVariable('NO_PROXY', $np, 'User')
    Write-Host "✓ 检测到代理，已把 ` + host + ` 设为直连（新开的终端生效）"
  }
}

if (Get-Command codex -ErrorAction SilentlyContinue) {
  Write-Host '完成。重新打开 Codex 就会用公司网关。'
} else {
  Write-Host '完成。还没装 Codex：npm install -g @openai/codex，装好后运行 codex 即可。'
}
Write-Host '提示：用 Clash 等代理软件的话，请把 ` + host + ` 设为直连（DIRECT）。'
`
}
