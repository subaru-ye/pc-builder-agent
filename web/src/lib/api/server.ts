import type { PublicBuildView } from "./types";

const tokenPattern = /^[A-Za-z0-9_-]{43}$/;

export async function fetchPublicShare(token: string): Promise<PublicBuildView | null> {
  if (!tokenPattern.test(token)) return null;
  const base = (process.env.GO_API_BASE_URL ?? "http://localhost:8082").replace(/\/$/, "");
  const response = await fetch(`${base}/api/v1/public/shares/${encodeURIComponent(token)}`, {
    cache: "no-store",
    headers: { Accept: "application/json" },
  });
  if (response.status === 404) return null;
  if (!response.ok) throw new Error(`public share upstream returned ${response.status}`);
  return response.json() as Promise<PublicBuildView>;
}

export function configuredWebBaseURL() {
  try { return new URL(process.env.PUBLIC_WEB_BASE_URL ?? "http://localhost:3000"); }
  catch { return new URL("http://localhost:3000"); }
}
