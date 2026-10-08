import iconCodex from "../assets/providers/codex.svg";
import iconKimi from "../assets/providers/kimi-light.svg";
import iconAntigravity from "../assets/providers/antigravity.svg";
import iconGrok from "../assets/providers/grok.svg";
import iconMeta from "../assets/providers/meta.svg";
import iconDevin from "../assets/providers/devin.svg";

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

const ICONS: Record<string, string> = {
  codex: iconCodex,
  kimi: iconKimi,
  "kimi-ai": iconKimi,
  antigravity: iconAntigravity,
  xai: iconGrok,
  meta: iconMeta,
  devin: iconDevin,
};

// providerIcon is the provider's logo (icons from CPA's management center).
export function providerIcon(p?: string): string | undefined {
  return ICONS[p || "codex"];
}
