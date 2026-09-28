import { useEffect, useState } from "react";

// The entry bundle is content-hashed, so a redeploy changes its URL in the
// served index.html. A tab left open keeps running the old bundle until it is
// reloaded; this banner tells the user when that happens.
const entryRe = /src="(\/assets\/index-[^"]+\.js)"/;

function loadedEntry(): string | null {
  for (const s of Array.from(document.scripts)) {
    const m = s.getAttribute("src")?.match(/^\/assets\/index-[^"]+\.js$/);
    if (m) return m[0];
  }
  return null;
}

export function UpdateBanner() {
  const [stale, setStale] = useState(false);

  useEffect(() => {
    const mine = loadedEntry();
    if (!mine) return; // dev server: no hashed bundle
    const check = async () => {
      try {
        const html = await (await fetch("/", { cache: "no-store" })).text();
        const served = html.match(entryRe)?.[1];
        if (served && served !== mine) setStale(true);
      } catch {
        /* offline: try again later */
      }
    };
    const timer = window.setInterval(check, 5 * 60 * 1000);
    window.addEventListener("focus", check);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", check);
    };
  }, []);

  if (!stale) return null;
  return (
    <div className="bg-indigo-600 px-4 py-2 text-center text-sm text-white">
      网关已更新到新版本，
      <button
        onClick={() => window.location.reload()}
        className="font-semibold underline underline-offset-2"
      >
        点此刷新
      </button>
      后即可使用。
    </div>
  );
}
