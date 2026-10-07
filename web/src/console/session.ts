import { createReplayClient } from "../shared/contracts/replayClient";
import type { components } from "../shared/contracts/generated/replay";

export type ReplayRecord = components["schemas"]["RecordSummary"];
export type RecordDetailData = components["schemas"]["RecordDetail"];
export type Identity = components["schemas"]["Identity"];
export type Session = ReturnType<typeof createSession>;
export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
// Callers suppress cancellations they own; unexpected interruptions stay visible.
export function requestError(caught: unknown, fallback: string): string {
  if (caught instanceof APIError) {
    if (caught.status === 401)
      return "Your API key was rejected. Sign in again with a valid operator key.";
    if (caught.status === 403)
      return "This API key does not have permission for this request. Use a key with the required scope.";
    if (caught.status === 429)
      return "The replay service is limiting requests. Wait a moment, then try again.";
    if (caught.status >= 500)
      return "The replay service could not complete the request. Check service health, then try again.";
    return caught.message;
  }
  if (
    (caught instanceof Error || caught instanceof DOMException) &&
    caught.name === "AbortError"
  )
    return "The request was interrupted before a response was received. Try again.";
  if (caught instanceof TypeError)
    return "The replay service could not be reached. Check your connection, service availability and API CORS configuration, then try again.";
  return caught instanceof Error ? caught.message : fallback;
}
export function result<T>(response: {
  data?: T;
  error?: unknown;
  response: Response;
}): T {
  if (!response.response.ok) {
    const error = response.error as
      { detail?: string; code?: string } | undefined;
    throw new APIError(
      response.response.status,
      error?.detail ||
        error?.code ||
        `Request failed (${response.response.status}).`,
    );
  }
  if (response.data === undefined)
    throw new Error("The API returned no result.");
  return response.data;
}

// The credential and all requests belong to one disposable browser-memory session.
export function createSession(credential: string, unauthorized: () => void, apiUrl: string) {
  let token: string | undefined = credential;
  const controller = new AbortController();
  const endpoint = new URL(apiUrl);
  const baseUrl = apiUrl.replace(/\/$/, "");
  const apiPath = new URL(`${baseUrl}/v1/`).pathname;
  const client = createReplayClient({
    baseUrl,
    token: () => token,
    fetch: async (request) => {
      const target = new URL(request.url);
      if (target.origin !== endpoint.origin || !target.pathname.startsWith(apiPath))
        throw new Error("The request is outside the configured replay API.");
      const response = await fetch(
        new Request(request, {
          cache: "no-store",
          credentials: "omit",
          redirect: "error",
        }),
      );
      if (response.status === 401) {
        // Disposal aborts this response body too. Preserve the authentication
        // failure before the generated client tries to read that aborted body.
        unauthorized();
        throw new APIError(401, "The API key was rejected.");
      }
      return response;
    },
  });
  return {
    client,
    signal: controller.signal,
    dispose() {
      token = undefined;
      controller.abort();
    },
  };
}
export function deliveryStatusLabel(f: Pick<ReplayRecord, "delivery_status" | "resolution_source">) {
  if (f.delivery_status === "resolved")
    return f.resolution_source === "operator_confirmed"
      ? "Resolved — operator confirmed"
      : "Resolved — retry succeeded";
  return ({ open: "Open", retrying: "Retry in progress", unknown: "Unknown outcome" } as Record<string, string>)[f.delivery_status || ""] || "Delivery state unavailable";
}
export function statusLabel(f: Pick<ReplayRecord, "kind" | "delivery_status" | "resolution_source" | "dismissed_at">) {
  if (f.dismissed_at) return "Dismissed";
  if (f.kind === "inspection") return "Available";
  return deliveryStatusLabel(f);
}

export function timestamp(value?: string) {
  if (!value) return "—";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
}
