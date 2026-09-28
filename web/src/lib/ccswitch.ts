// CC Switch (github.com/farion1231/cc-switch) imports a provider from a
// ccswitch://v1/import link after showing a confirmation dialog. For Codex it
// keeps only the key, endpoint and model and writes its own config.toml
// (the key goes to auth.json as OPENAI_API_KEY, with requires_openai_auth),
// so nothing else is worth sending. The key never leaves the browser except
// into this local link.

export interface CCSwitchTarget {
  name: string;
  endpoint: string;
  homepage: string;
}

export const CCSWITCH_DOWNLOAD =
  "https://github.com/farion1231/cc-switch/releases/latest";

export function ccSwitchLink(
  t: CCSwitchTarget,
  apiKey: string,
  model: string,
): string {
  // URLSearchParams form-encodes ("+" becomes %2B, spaces become +), which
  // is exactly how CC Switch decodes the query.
  const q = new URLSearchParams({
    resource: "provider",
    app: "codex",
    name: t.name,
    endpoint: t.endpoint,
    homepage: t.homepage,
    apiKey: apiKey.trim(),
    model,
    enabled: "true", // switch Codex to it right after the user confirms
  });
  return `ccswitch://v1/import?${q.toString()}`;
}

export function looksLikeKey(k: string): boolean {
  return /^sk-[A-Za-z0-9_-]{16,}$/.test(k.trim());
}
