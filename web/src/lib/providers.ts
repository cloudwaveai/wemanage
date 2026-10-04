// Account providers (CPA auth-file types) as people call them.
const NAMES: Record<string, string> = {
  codex: "ChatGPT",
  kimi: "Kimi Code",
  "kimi-ai": "Kimi Code 国际版",
  antigravity: "Antigravity",
  xai: "Grok",
  meta: "Meta Muse",
  devin: "Devin",
};

export function providerName(p?: string): string {
  return NAMES[p || "codex"] ?? p ?? "ChatGPT";
}

export function isChatGPT(p?: string): boolean {
  return !p || p === "codex";
}

// hasQuota: providers whose 5-hour / weekly quota the gateway reads.
export function hasQuota(p?: string): boolean {
  return isChatGPT(p) || p === "kimi";
}

// accountName names an account: its email, else its provider and id
// (Kimi logins carry no email).
export function accountName(a: { email?: string; provider?: string; id: number }): string {
  return a.email || `${providerName(a.provider)} #${a.id}`;
}
