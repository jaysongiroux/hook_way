export interface RuntimeConfig { replayApiUrl: string }

export function parseRuntimeConfig(value: unknown): RuntimeConfig {
  const raw = (value as Partial<RuntimeConfig> | null)?.replayApiUrl;
  if (typeof raw !== "string" || !raw || raw !== raw.trim())
    throw new Error("REPLAY_API_URL must be configured as an absolute HTTP or HTTPS API base URL.");
  let url: URL;
  try { url = new URL(raw); } catch {
    throw new Error("REPLAY_API_URL must be an absolute HTTP or HTTPS URL.");
  }
  if (!/^https?:$/.test(url.protocol) || url.username || url.password || url.search || url.hash || raw.includes("?") || raw.includes("#") || /[\\\\\s]/.test(raw))
    throw new Error("REPLAY_API_URL must use HTTP or HTTPS without credentials, a query or a fragment.");
  return { replayApiUrl: url.href.replace(/\/$/, "") };
}

export async function loadRuntimeConfig(): Promise<RuntimeConfig> {
  const response = await fetch("/runtime-config.json", { cache: "no-store", credentials: "omit", redirect: "error" });
  if (!response.ok) throw new Error(`Runtime configuration could not be loaded (${response.status}).`);
  return parseRuntimeConfig(await response.json());
}
