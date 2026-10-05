import { describe, expect, it, vi } from "vitest";
import { createEditorClient } from "./editorClient";
import { createReplayClient } from "./replayClient";

describe("generated API clients", () => {
  it("keeps credentials separate, reads fresh tokens, and sends exact YAML bytes", async () => {
    const requests: Request[] = [];
    const transport = vi.fn(async (request: Request) => {
      requests.push(request);
      return new Response(JSON.stringify({ issues: [] }), {
        headers: { "Content-Type": "application/json" },
      });
    });
    let editorToken = "editor-a";
    const editor = createEditorClient({
      baseUrl: "https://builder.example",
      token: () => editorToken,
      fetch: transport,
    });
    const replay = createReplayClient({
      baseUrl: "https://replay.example",
      token: () => "replay-only",
      fetch: transport,
    });
    const yaml = "flows:\n  example: {}\n";
    await editor.POST("/validate", {
      body: yaml,
      bodySerializer: (body) => body,
      headers: { "Content-Type": "application/yaml" },
    });
    editorToken = "editor-b";
    await editor.GET("/contract");
    await replay.GET("/v1/failures");
    expect(requests.map((r) => r.headers.get("Authorization"))).toEqual([
      "Bearer editor-a",
      "Bearer editor-b",
      "Bearer replay-only",
    ]);
    expect(requests.map((r) => r.url)).toEqual([
      "https://builder.example/validate",
      "https://builder.example/contract",
      "https://replay.example/v1/failures",
    ]);
    expect(await requests[0].text()).toBe(yaml);
    expect(requests[0].headers.get("Content-Type")).toBe("application/yaml");
    expect(requests.every((r) => r.credentials === "omit")).toBe(true);
  });

  it("retains mixed batch outcomes and structured problem details", async () => {
    const outcomes = {
      results: [
        { failure_id: "one", status: 202, run_id: "run" },
        {
          failure_id: "two",
          status: 404,
          code: "not_found",
          message: "Missing",
        },
      ],
    };
    const transport = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(outcomes), {
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            type: "about:blank",
            title: "Conflict",
            status: 409,
            code: "unknown_outcome",
            detail: "Confirm duplicate risk",
          }),
          {
            status: 409,
            headers: { "Content-Type": "application/problem+json" },
          },
        ),
      );
    const client = createReplayClient({ fetch: transport });
    const batch = await client.POST("/v1/retries", {
      params: { header: { "Idempotency-Key": "batch" } },
      body: { failure_ids: ["one", "two"] },
    });
    expect(batch.data).toEqual(outcomes);
    const retry = await client.POST("/v1/failures/{id}/retries", {
      params: { path: { id: "one" }, header: { "Idempotency-Key": "retry" } },
      body: {},
    });
    expect(retry.response.status).toBe(409);
    expect(retry.error).toMatchObject({ code: "unknown_outcome" });
  });

  it("downloads arbitrary payload bytes without JSON conversion", async () => {
    const bytes = new Uint8Array([0, 255, 10, 17]);
    const client = createReplayClient({
      fetch: vi.fn(
        async () =>
          new Response(bytes, {
            headers: { "Content-Type": "application/octet-stream" },
          }),
      ),
    });
    const result = await client.GET("/v1/failures/{id}/payload", {
      params: { path: { id: "payload" } },
      parseAs: "arrayBuffer",
    });
    expect(new Uint8Array(result.data!)).toEqual(bytes);
  });
});
