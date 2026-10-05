export interface APIClientOptions {
  baseUrl?: string;
  token?: () => string | undefined;
  fetch?: (request: Request) => Promise<Response>;
}

// Each client owns its token callback. Credentials are read at request time,
// never persisted, and never inherited from the other application's client.
export function clientOptions(options: APIClientOptions) {
  const baseUrl =
    options.baseUrl ??
    (typeof window === "undefined"
      ? "http://localhost"
      : window.location.origin);
  return {
    baseUrl,
    credentials: "omit" as const,
    fetch: async (request: Request) => {
      const token = options.token?.();
      if (token) request.headers.set("Authorization", `Bearer ${token}`);
      return (options.fetch ?? globalThis.fetch)(request);
    },
  };
}
