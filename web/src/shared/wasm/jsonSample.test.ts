import { configureEditorValidation } from "./validationMode";
import { beforeEach, afterEach, expect, test, vi } from "vitest";

const loader = vi.hoisted(() => ({
  loadWasm: vi.fn(),
  hasWasmCapability: vi.fn(),
  invokeWasm: vi.fn(),
}));
vi.mock("./loader", () => loader);

beforeEach(() => {
  configureEditorValidation("api-fallback");
  loader.loadWasm.mockReset().mockResolvedValue({});
  loader.hasWasmCapability.mockReset().mockReturnValue(true);
  loader.invokeWasm.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

const input = {
  schema: { type: "object", properties: { id: { type: "integer" } } },
  sample: '{"id":9007199254740993}',
};

test("WASM sample validation sends raw JSON text and returns output text unchanged", async () => {
  const output = '{"id":9007199254740993}';
  loader.invokeWasm.mockReturnValue(JSON.stringify({ valid: true, issues: [], output }));
  const { validateJSONSample } = await import("./jsonSample");
  expect(await validateJSONSample(input)).toEqual({ valid: true, issues: [], output });
  expect(loader.invokeWasm.mock.calls[0][0]).toBe("json_sample_validate");
  expect(JSON.parse(loader.invokeWasm.mock.calls[0][1]).sample).toBe(input.sample);
});

test("missing WASM capability falls back to the generated editor client", async () => {
  loader.hasWasmCapability.mockReturnValue(false);
  const fetch = vi.fn(async (_request: Request) => new Response(JSON.stringify({
    valid: false,
    issues: [{ field: "sample", path: "/id", keyword: "type", message: "expected integer" }],
  }), { headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetch);
  const { validateJSONSample } = await import("./jsonSample");
  expect(await validateJSONSample(input)).toMatchObject({ valid: false, issues: [{ path: "/id" }] });
  const request = fetch.mock.calls[0][0];
  expect(new URL(request.url).pathname).toBe("/json-sample-validation");
  expect(request.method).toBe("POST");
  expect((await request.clone().json()).sample).toBe(input.sample);
});

test("unavailable local and API validators return null", async () => {
  loader.loadWasm.mockRejectedValue(new Error("offline"));
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
  const { validateJSONSample } = await import("./jsonSample");
  expect(await validateJSONSample(input)).toBeNull();
});

test("malformed success responses never appear as valid", async () => {
  loader.invokeWasm.mockReturnValue(JSON.stringify({ valid: true, issues: [] }));
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ valid: true, issues: [{ field: "sample" }] }), {
    headers: { "Content-Type": "application/json" },
  })));
  const { validateJSONSample } = await import("./jsonSample");
  expect(await validateJSONSample(input)).toBeNull();
});


test("product mode reports unavailable sample validation without contacting an editor API", async () => {
  configureEditorValidation("local");
  loader.hasWasmCapability.mockReturnValue(false);
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  const { validateJSONSample } = await import("./jsonSample");
  expect(await validateJSONSample(input)).toBeNull();
  expect(fetch).not.toHaveBeenCalled();
});
