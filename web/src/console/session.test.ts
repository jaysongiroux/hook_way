import { afterEach, describe, expect, it, vi } from "vitest";
import { APIError, createSession, requestError, result, statusLabel, deliveryStatusLabel } from "./session";

afterEach(() => vi.unstubAllGlobals());
describe("console request failures", () => {
  it("preserves a typed authentication failure when disposal aborts the response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("rejected", { status: 401 })),
    );
    let session: ReturnType<typeof createSession>;
    const unauthorized = vi.fn(() => session.dispose());
    session = createSession("synthetic-token", unauthorized, "http://localhost");
    await expect(
      session.client.GET("/v1/me", { signal: session.signal }),
    ).rejects.toMatchObject({ status: 401 });
    expect(unauthorized).toHaveBeenCalledOnce();
    expect(session.signal.aborted).toBe(true);
  });
  it("targets only the configured API prefix with bearer credentials and no cookies or redirects", async () => {
    const fetch = vi.fn(async (_request: Request) => new Response(JSON.stringify({ key_name: "operator" }), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetch);
    const session = createSession("synthetic-token", () => {}, "https://api.example/replay");
    await session.client.GET("/v1/me");
    const request = fetch.mock.calls[0][0] as Request;
    expect(request.url).toBe("https://api.example/replay/v1/me");
    expect(request.headers.get("Authorization")).toBe("Bearer synthetic-token");
    expect(request.credentials).toBe("omit");
    expect(request.redirect).toBe("error");
    session.dispose();
  });
  it("gives recovery guidance for network, interruption and permission failures", () => {
    expect(
      requestError(
        new DOMException("signal is aborted without reason", "AbortError"),
        "failed",
      ),
    ).toContain("interrupted");
    expect(requestError(new TypeError("Failed to fetch"), "failed")).toContain(
      "connection",
    );
    expect(requestError(new APIError(403, "forbidden"), "failed")).toContain(
      "required scope",
    );
    expect(
      requestError(new APIError(503, "private database exception"), "failed"),
    ).not.toContain("private");
  });
  it("retains actionable conflicts from structured API problems", () => {
    expect(() =>
      result({
        response: new Response(null, { status: 409 }),
        error: {
          code: "state_conflict",
          detail: "Another operator queued a retry. Refresh this record.",
        },
      }),
    ).toThrow("Another operator queued a retry");
  });
});

describe("record state labels", () => {
  it("keeps dismissal separate from the retained delivery evidence", () => {
    const record = { kind: "failure" as const, delivery_status: "unknown" as const, dismissed_at: "2026-10-06T12:00:00Z" };
    expect(statusLabel(record)).toBe("Dismissed");
    expect(deliveryStatusLabel(record)).toBe("Unknown outcome");
  });
  it("labels inspections without fabricating a delivery state", () => {
    expect(statusLabel({ kind: "inspection" })).toBe("Available");
    expect(statusLabel({ kind: "inspection", dismissed_at: "2026-10-06T12:00:00Z" })).toBe("Dismissed");
  });
});
